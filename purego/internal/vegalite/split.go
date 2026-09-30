package vegalite

import "github.com/mgilbir/aster/purego/internal/jsval"

// withExplicit is upstream's Explicit<T>: a value plus whether the user wrote it.
type withExplicit struct {
	explicit bool
	value    Value
}

func makeExplicit(v Value) withExplicit { return withExplicit{true, v} }
func makeImplicit(v Value) withExplicit { return withExplicit{false, v} }

// split holds a component's properties, separated into user-specified
// (explicit) and derived (implicit) ones. combine() lists explicit keys first,
// which is why the order in which properties are set decides output order.
type split struct {
	explicit, implicit *Object
}

func newSplit() *split { return &split{jsval.NewObject(4), jsval.NewObject(4)} }

func (s *split) clone() *split {
	return &split{deepClone(jsval.Obj(s.explicit)).ObjValue(), deepClone(jsval.Obj(s.implicit)).ObjValue()}
}

func (s *split) combine() *Object {
	o := jsval.NewObject(s.explicit.Len() + s.implicit.Len())
	spread(o, jsval.Obj(s.explicit), jsval.Obj(s.implicit))
	return o
}

func (s *split) get(key string) Value {
	return firstDefined(s.explicit.Lookup(key), s.implicit.Lookup(key))
}

func (s *split) getWithExplicit(key string) withExplicit {
	if v := s.explicit.Lookup(key); !v.IsUndefined() {
		return withExplicit{true, v}
	}
	if v := s.implicit.Lookup(key); !v.IsUndefined() {
		return withExplicit{false, v}
	}
	return withExplicit{false, undef}
}

func (s *split) setWithExplicit(key string, w withExplicit) {
	if !w.value.IsUndefined() {
		s.set(key, w.value, w.explicit)
	}
}

func (s *split) set(key string, v Value, explicit bool) {
	if explicit {
		s.implicit.Delete(key)
		jsSet(s.explicit, key, v)
	} else {
		s.explicit.Delete(key)
		jsSet(s.implicit, key, v)
	}
}

func (s *split) copyKeyFromObject(key string, o Value) {
	if v := o.Get(key); !v.IsUndefined() {
		s.set(key, v, true)
	}
}

func (s *split) copyAll(o *split) {
	comb := o.combine()
	for _, k := range comb.Keys() {
		s.setWithExplicit(k, o.getWithExplicit(k))
	}
}

// tieBreaker resolves two conflicting explicit/implicit values.
type tieBreaker func(v1, v2 withExplicit, property, propertyOf string) withExplicit

func defaultTieBreaker(v1, v2 withExplicit, _, _ string) withExplicit { return v1 }

func tieBreakByComparing(compare func(a, b Value) int) tieBreaker {
	return func(v1, v2 withExplicit, property, propertyOf string) withExplicit {
		diff := compare(v1.value, v2.value)
		if diff > 0 {
			return v1
		} else if diff < 0 {
			return v2
		}
		return defaultTieBreaker(v1, v2, property, propertyOf)
	}
}

// mergeValuesWithExplicit merges two values of one property: explicit beats
// implicit, equal values collapse, otherwise the tie breaker decides. v1 may be
// nil (absent).
func mergeValuesWithExplicit(v1 *withExplicit, v2 withExplicit, property, propertyOf string, tb tieBreaker) withExplicit {
	if tb == nil {
		tb = defaultTieBreaker
	}
	if v1 == nil || v1.value.IsUndefined() {
		return v2
	}
	switch {
	case v1.explicit && !v2.explicit:
		return *v1
	case v2.explicit && !v1.explicit:
		return v2
	case deepEqual(v1.value, v2.value):
		return *v1
	}
	return tb(*v1, v2, property, propertyOf)
}
