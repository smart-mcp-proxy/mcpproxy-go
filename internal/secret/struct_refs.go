package secret

import "reflect"

// StructFileRefPaths returns the path of every ${file:} reference in the
// fields ExpandStructSecretsCollectErrors would expand in v, in walk order.
// It visits exactly what that walk visits (exported struct fields through
// pointers, slices, arrays, map values that are strings or interfaces holding
// strings) and nothing else, so an opaque carrier such as a struct with only
// unexported fields contributes nothing. Nothing is resolved or read.
// v must be a pointer, as for ExpandStructSecretsCollectErrors.
func StructFileRefPaths(v interface{}) []string {
	var out []string
	walkStructRefStrings(reflect.ValueOf(v), func(s string) {
		if !IsSecretRef(s) {
			return
		}
		for _, ref := range FindSecretRefs(s) {
			if ref.Type == SecretTypeFile {
				out = append(out, ref.Name)
			}
		}
	})
	return out
}

// walkStructRefStrings mirrors expandValueCollectErrors' traversal.
func walkStructRefStrings(v reflect.Value, fn func(string)) {
	if !v.IsValid() {
		return
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		walkStructRefStrings(v.Elem(), fn)
		return
	}
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			fn(v.String())
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if !field.CanInterface() || !t.Field(i).IsExported() {
				continue
			}
			walkStructRefStrings(field, fn)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walkStructRefStrings(v.Index(i), fn)
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			mv := v.MapIndex(key)
			switch {
			case mv.Kind() == reflect.String:
				fn(mv.String())
			case mv.Kind() == reflect.Interface && mv.Elem().Kind() == reflect.String:
				fn(mv.Elem().String())
			}
		}
	}
}
