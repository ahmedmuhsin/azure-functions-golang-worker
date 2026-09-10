// Package bindingtype provides cycle-safe type inspection for generic bindings.
package bindingtype

import (
	"fmt"
	"reflect"
	"strings"
)

// Base unwraps pointers, rejecting pointer-only cycles which have no data type.
func Base(t reflect.Type) (reflect.Type, error) {
	seen := make(map[reflect.Type]bool)
	for t.Kind() == reflect.Ptr {
		if seen[t] {
			return nil, fmt.Errorf("payload cannot contain recursive pointer type %s", t)
		}
		seen[t] = true
		t = t.Elem()
	}
	return t, nil
}

// Validate checks pointer chains throughout a payload shape. Revisiting a
// struct, map, or slice is allowed: ordinary recursive models terminate as
// their finite JSON data is consumed. A pointer-only cycle cannot do so.
func Validate(t reflect.Type) error {
	visited := make(map[reflect.Type]bool)
	var visit func(reflect.Type) error
	visit = func(t reflect.Type) error {
		base, err := Base(t)
		if err != nil {
			return err
		}
		if visited[base] {
			return nil
		}
		visited[base] = true
		switch base.Kind() {
		case reflect.Slice, reflect.Array:
			return visit(base.Elem())
		case reflect.Map:
			if err := visit(base.Key()); err != nil {
				return err
			}
			return visit(base.Elem())
		case reflect.Struct:
			for i := 0; i < base.NumField(); i++ {
				field := base.Field(i)
				if !field.IsExported() || strings.Split(field.Tag.Get("json"), ",")[0] == "-" {
					continue
				}
				if err := visit(field.Type); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(t)
}
