package bindingtype

import (
	"reflect"
	"testing"
)

type loop *loop
type node struct {
	Next     *node
	Children []*node
}

func TestValidatePointerCycles(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[loop](), reflect.TypeFor[[]loop](), reflect.TypeFor[map[string]loop](), reflect.TypeFor[struct{ Value loop }]()} {
		if err := Validate(typ); err == nil {
			t.Errorf("accepted recursive pointer type %s", typ)
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[node](), reflect.TypeFor[[]*node](), reflect.TypeFor[**string](), reflect.TypeFor[map[string]any]()} {
		if err := Validate(typ); err != nil {
			t.Errorf("rejected valid type %s: %v", typ, err)
		}
	}
}

func TestBase(t *testing.T) {
	if _, err := Base(reflect.TypeFor[loop]()); err == nil {
		t.Fatal("accepted recursive pointer")
	}
	if got, err := Base(reflect.TypeFor[**node]()); err != nil || got != reflect.TypeFor[node]() {
		t.Fatalf("base=%v error=%v", got, err)
	}
}
