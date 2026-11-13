package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestHookDependenciesOrderAndRollback(t *testing.T) {
	var events []string
	failure := errors.New("compose failed")
	makeHook := func(name string, dependencies ...string) Hook {
		return Hook{Name: name, DependsOn: dependencies, Start: func(context.Context) error {
			events = append(events, name+"+")
			if name == "compose" {
				return failure
			}
			return nil
		}, Stop: func(context.Context) error { events = append(events, name+"-"); return nil }}
	}
	hooks := []Hook{makeHook("compose", "db", "cache"), makeHook("cache", "db"), makeHook("db"), makeHook("never", "compose")}
	a, err := New(&testService{run: func() error { t.Fatal("served after startup failure"); return nil }}, Options{Hooks: hooks})
	if err != nil {
		t.Fatal(err)
	}
	hooks[0].DependsOn[0] = "missing"
	if err := a.Run(context.Background()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if want := []string{"db+", "cache+", "compose+", "compose-", "cache-", "db-"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("%v", events)
	}
}

func TestInvalidHookGraphFailsWithoutStarting(t *testing.T) {
	callback := func(context.Context) error { t.Fatal("started during construction"); return nil }
	for _, hooks := range [][]Hook{
		{{Name: "a", Start: callback, DependsOn: []string{"missing"}}},
		{{Name: "a", Start: callback, DependsOn: []string{"a"}}},
		{{Name: "a", Start: callback, DependsOn: []string{"b"}}, {Name: "b", Start: callback, DependsOn: []string{"a"}}},
		{{Name: "a", Start: callback}, {Name: "b", Start: callback, DependsOn: []string{"a", "a"}}},
	} {
		if _, err := New(&testService{}, Options{Hooks: hooks}); err == nil {
			t.Fatalf("accepted %v", hooks)
		}
	}
}
