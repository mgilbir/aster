package vega

import (
	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// This file implements the expression environment's view functions for a
// headless render: there is no container, window or pointer event, so the
// functions answer what upstream answers without a DOM.

// EventFunction implements expr.ViewProvider: event.vega.<name>(...) reads a
// pointer event, and there is none.
func (c *rtContext) EventFunction(name string, args []jsval.Value) jsval.Value {
	return jsval.Undefined
}

// ContainerSize is [clientWidth, clientHeight] of the view's container.
func (c *rtContext) ContainerSize() jsval.Value {
	return jsval.ArrOf(jsval.Undefined, jsval.Undefined)
}

// Screen is window.screen.
func (c *rtContext) Screen() jsval.Value { return jsval.Obj(jsval.NewObject(0)) }

// WindowSize is [innerWidth, innerHeight].
func (c *rtContext) WindowSize() jsval.Value {
	return jsval.ArrOf(jsval.Undefined, jsval.Undefined)
}

// Encode is encode(item, name, retval): it requests re-encoding of an item,
// which a static render never needs; the result is retval or the item.
func (c *rtContext) Encode(item, name, retval jsval.Value) jsval.Value {
	if !retval.IsUndefined() {
		return retval
	}
	return item
}

// itemHandle gives an item a stable id that survives in its expression view.
func (v *runView) itemHandle(it *scene.Item) int {
	if v.itemIDs == nil {
		v.itemIDs = map[*scene.Item]int{}
	}
	id, ok := v.itemIDs[it]
	if !ok {
		id = len(v.itemByID)
		v.itemIDs[it] = id
		v.itemByID = append(v.itemByID, it)
	}
	return id
}

// InScope is inScope(item): whether the item lies within the group this
// context draws into. Upstream walks `item = item.mark.group` up from the
// object it is given, so a value that is not an item (an object an
// expression made, a number) reads mark of undefined: a TypeError. The item
// is found by identity, as upstream compares it, not by a number it carries.
func (c *rtContext) InScope(item jsval.Value) bool {
	if c.group == nil || !item.IsTruthy() {
		return false
	}
	it := c.view.itemOf(item)
	if it == nil {
		panic(&expr.Error{Name: "TypeError", Msg: "Cannot read properties of undefined (reading 'group')"})
	}
	for it != nil {
		if it == c.group {
			return true
		}
		if it.Mark == nil {
			break
		}
		it = it.Mark.Group
	}
	return false
}

// itemOf is the scenegraph item a value presents (itemAsValue, itemTuple),
// or nil when it presents none.
func (v *runView) itemOf(x jsval.Value) *scene.Item {
	o := x.ObjValue()
	if o == nil {
		return nil
	}
	if it := v.viewItem[o]; it != nil {
		return it
	}
	it, _ := o.Host().(*scene.Item)
	return it
}

// Intersect and IntersectLasso select items by a pixel region, which needs an
// interaction the static render does not have.
func (c *rtContext) Intersect(bounds, opt, group jsval.Value) jsval.Value { return jsval.Undefined }

func (c *rtContext) IntersectLasso(markname, lasso, unit jsval.Value) jsval.Value {
	return jsval.Undefined
}
