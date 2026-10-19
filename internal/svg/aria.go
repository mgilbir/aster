package svg

import (
	"github.com/mgilbir/aster/internal/scene"
)

const (
	graphicsObject = "graphics-object"
	graphicsSymbol = "graphics-symbol"
)

// ariaIgnore lists guide roles whose accessibility is covered by their parent
// guide, so they get no aria attributes of their own.
func ariaIgnored(role string) bool {
	switch role {
	case "axis-domain", "axis-grid", "axis-label", "axis-tick", "axis-title",
		"legend-band", "legend-entry", "legend-gradient", "legend-label",
		"legend-title", "legend-symbol", "title":
		return true
	}
	return false
}

// markAria writes the aria attributes of a mark's <g> container.
func (r *renderer) markAria(m *scene.Mark) {
	w := &r.w
	if m.Aria.IsFalse() {
		w.attrLit("aria-hidden", "true")
		return
	}
	if ariaIgnored(m.Role) {
		return
	}
	var role, desc, label string
	switch m.Role {
	case "axis", "legend":
		if len(m.Items) == 0 {
			return // upstream throws reading items[0] and emits nothing
		}
		role, desc = graphicsSymbol, m.Role
		label = m.Description
		if label == "" {
			label = m.Items[0].Description
		}
		if label == "" {
			if m.NoGuideCaption {
				return // upstream's caption throws; see scene.Mark.NoGuideCaption
			}
			label = m.GuideCaption
		}
	case "title-text", "title-subtitle":
		if len(m.Items) == 0 {
			return
		}
		role = graphicsSymbol
		kind, prefix := "title", "Title text '"
		if m.Role == "title-subtitle" {
			kind, prefix = "subtitle", "Subtitle text '"
		}
		desc = kind
		label = m.Description
		if label == "" {
			label = m.Items[0].Description
		}
		if label == "" {
			w.attr("role", role)
			w.attr("aria-roledescription", desc)
			w.attrOpen("aria-label")
			w.valStr(prefix)
			w.titleCaption(m.Items[0])
			w.valLit("'")
			w.attrClose()
			return
		}
	default:
		t := m.Type
		recurse := t == scene.MarkGroup || t == scene.MarkText
		if !recurse {
			for _, it := range m.Items {
				if it.Description != "" && !it.Aria.IsFalse() {
					recurse = true
					break
				}
			}
		}
		role = graphicsSymbol
		if recurse {
			role = graphicsObject
		}
		desc = t.String() + " mark container"
		label = m.Description
	}
	w.attr("role", role)
	w.attr("aria-roledescription", desc)
	if label != "" {
		w.attr("aria-label", label)
	}
}

// titleCaption writes `array(item.text).join(' ')`, an entry at a time (see
// writer.valJS).
func (w *writer) titleCaption(it *scene.Item) {
	t := it.Text()
	if !t.IsArr() {
		if !t.IsNullish() {
			w.valJS(t)
		}
		return
	}
	for i, v := range t.Items() {
		if i > 0 {
			w.valLit(" ")
		}
		if !v.IsNullish() {
			w.valJS(v)
		}
	}
}

// itemAria writes the aria attributes of an item element.
func (r *renderer) itemAria(m *scene.Mark, it *scene.Item) {
	w := &r.w
	if it.Aria.IsFalse() {
		w.attrLit("aria-hidden", "true")
		return
	}
	if it.Description == "" {
		return
	}
	typ := m.Type
	w.attr("aria-label", it.Description)
	role := it.AriaRole
	if role == "" {
		if typ == scene.MarkGroup {
			role = graphicsObject
		} else {
			role = graphicsSymbol
		}
	}
	w.attr("role", role)
	rd := it.AriaRoleDescription
	if rd == "" {
		rd = typ.String() + " mark"
	}
	w.attr("aria-roledescription", rd)
}
