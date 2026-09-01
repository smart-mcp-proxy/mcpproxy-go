package oauth

import (
	"reflect"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Issue #1148, round 7 finding 4. The round-6 guard reflected over the TOP
// LEVEL of config.ServerConfig / contracts.Server only, so every defect that
// round found — isolation.extra_args published in the clear on two doors, its
// mask accepted on a third — lived in a NESTED field the guard never looked at.
//
// These walkers are the recursive replacement. They are the single definition
// of "what is on the wire under a server payload" that both the coverage guard
// and the stale-entry guard read, so the two cannot disagree about the shape
// they are enforcing.

var timeType = reflect.TypeOf(time.Time{})

// serverWireTypes are the two structs a server is published as.
func serverWireTypes() []struct {
	name string
	typ  reflect.Type
} {
	return []struct {
		name string
		typ  reflect.Type
	}{
		{"config.ServerConfig", reflect.TypeOf(config.ServerConfig{})},
		{"contracts.Server", reflect.TypeOf(contracts.Server{})},
	}
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

// carriesText reports whether a type can carry free text — and therefore a
// credential — anywhere inside it.
//
// This is DERIVED from the type rather than asserted by a human: a bool, an
// int, a time.Time or a duration structurally cannot hold a token, so demanding
// a redaction decision for one is noise. Everything that can hold one must have
// an answer.
func carriesText(t reflect.Type) bool {
	return carriesTextSeen(t, map[reflect.Type]bool{})
}

func carriesTextSeen(t reflect.Type, seen map[reflect.Type]bool) bool {
	t = deref(t)
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.String, reflect.Interface:
		return true
	case reflect.Slice, reflect.Array:
		return carriesTextSeen(t.Elem(), seen)
	case reflect.Map:
		return carriesTextSeen(t.Elem(), seen)
	case reflect.Struct:
		if t == timeType {
			return false
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			if name := jsonFieldName(f); name == "" || name == "-" {
				continue
			}
			if carriesTextSeen(f.Type, seen) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func joinPath(parent, field string) string {
	if parent == "" {
		return field
	}
	return parent + "." + field
}

// textLeafPaths returns the dotted wire path of every leaf under t that can
// carry text: `field`, `field.nested`, `slice[].nested`, `map{}.nested`. A
// []string or map[string]string is itself a leaf — the redaction rules act on
// the whole collection, keyed by its own name.
func textLeafPaths(t reflect.Type) []string {
	var out []string
	var walk func(path string, t reflect.Type, seen map[reflect.Type]bool)
	walk = func(path string, t reflect.Type, seen map[reflect.Type]bool) {
		t = deref(t)
		if seen[t] {
			return
		}
		switch t.Kind() {
		case reflect.String, reflect.Interface:
			out = append(out, path)
		case reflect.Slice, reflect.Array:
			elem := deref(t.Elem())
			if elem.Kind() == reflect.String {
				out = append(out, path)
				return
			}
			if !carriesText(elem) {
				return
			}
			walk(path+"[]", elem, childSeen(seen, t))
		case reflect.Map:
			elem := deref(t.Elem())
			if elem.Kind() == reflect.String {
				out = append(out, path)
				return
			}
			if !carriesText(elem) {
				return
			}
			walk(path+"{}", elem, childSeen(seen, t))
		case reflect.Struct:
			if t == timeType {
				return
			}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if !f.IsExported() {
					continue
				}
				name := jsonFieldName(f)
				if name == "" || name == "-" {
					continue
				}
				if !carriesText(f.Type) {
					continue
				}
				walk(joinPath(path, name), f.Type, childSeen(seen, t))
			}
		}
	}
	walk("", t, map[reflect.Type]bool{})
	return out
}

// allWirePaths returns every path a server payload can carry, text or not —
// the universe the stale-entry guard checks table keys against.
func allWirePaths(t reflect.Type) []string {
	var out []string
	var walk func(path string, t reflect.Type, seen map[reflect.Type]bool)
	walk = func(path string, t reflect.Type, seen map[reflect.Type]bool) {
		t = deref(t)
		if seen[t] {
			return
		}
		switch t.Kind() {
		case reflect.Slice, reflect.Array:
			elem := deref(t.Elem())
			if elem.Kind() == reflect.Struct && elem != timeType {
				walk(path+"[]", elem, childSeen(seen, t))
			}
		case reflect.Map:
			elem := deref(t.Elem())
			if elem.Kind() == reflect.Struct && elem != timeType {
				walk(path+"{}", elem, childSeen(seen, t))
			}
		case reflect.Struct:
			if t == timeType {
				return
			}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if !f.IsExported() {
					continue
				}
				name := jsonFieldName(f)
				if name == "" || name == "-" {
					continue
				}
				child := joinPath(path, name)
				out = append(out, child)
				walk(child, f.Type, childSeen(seen, t))
			}
		}
	}
	walk("", t, map[reflect.Type]bool{})
	return out
}

func childSeen(seen map[reflect.Type]bool, t reflect.Type) map[reflect.Type]bool {
	out := make(map[reflect.Type]bool, len(seen)+1)
	for k, v := range seen {
		out[k] = v
	}
	out[deref(t)] = true
	return out
}
