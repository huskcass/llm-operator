/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cases

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// checkKeys walks a decoded YAML tree beside t and rejects a mapping key
// that does not exactly equal a json name. encoding/json, which
// sigs.k8s.io/yaml uses, matches those names without regard to case.
// Types that implement json.Unmarshaler are leaves: their own decoder owns
// the value, and Kubernetes types that decode themselves fall in that set.
func checkKeys(tree any, t reflect.Type) error {
	if t == nil || tree == nil {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if hasUnmarshalJSON(t) {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := tree.(map[string]any)
		if !ok {
			return fmt.Errorf("mapping for %s has type %T", typeName(t), tree)
		}
		fields := jsonFields(t)
		for k, v := range m {
			ft, ok := fields[k]
			if !ok {
				return fmt.Errorf("key %q does not match a field (names are case-sensitive)", k)
			}
			if err := checkKeys(v, ft); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
		}
	case reflect.Slice, reflect.Array:
		items, ok := tree.([]any)
		if !ok {
			return fmt.Errorf("sequence for %s has type %T", typeName(t), tree)
		}
		for i, item := range items {
			if err := checkKeys(item, t.Elem()); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil
		}
		m, ok := tree.(map[string]any)
		if !ok {
			return fmt.Errorf("mapping for %s has type %T", typeName(t), tree)
		}
		for k, v := range m {
			if err := checkKeys(v, t.Elem()); err != nil {
				return fmt.Errorf("[%s]: %w", k, err)
			}
		}
	default:
		return nil
	}
	return nil
}

func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	collectFields(out, t)
	return out
}

// collectFields records the json names encoding/json would accept, including
// fields promoted from an anonymous embedded struct.
func collectFields(out map[string]reflect.Type, t reflect.Type) {
	for f := range t.Fields() {
		if f.PkgPath != "" && !f.Anonymous {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		// encoding/json promotes an anonymous struct when the tag names no
		// field of its own. json:",inline" is that case: the name is empty.
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && !hasUnmarshalJSON(ft) {
				collectFields(out, ft)
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type
	}
}

func hasUnmarshalJSON(t reflect.Type) bool {
	u := reflect.TypeFor[json.Unmarshaler]()
	return t.Implements(u) || reflect.PointerTo(t).Implements(u)
}

func typeName(t reflect.Type) string {
	if t.Name() != "" {
		return t.Name()
	}
	return t.String()
}
