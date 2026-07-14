package sdk

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunInvocation_Success(t *testing.T) {
	inner := func(_ context.Context, _ *MiddlewareContext) error { return nil }
	rec, stack, err := RunInvocation(context.Background(), &MiddlewareContext{}, nil, inner)
	if rec != nil || stack != "" || err != nil {
		t.Fatalf("RunInvocation success = (%v, %q, %v), want all zero", rec, stack, err)
	}
}

func TestRunInvocation_Error(t *testing.T) {
	want := errors.New("boom")
	inner := func(_ context.Context, _ *MiddlewareContext) error { return want }
	rec, stack, err := RunInvocation(context.Background(), &MiddlewareContext{}, nil, inner)
	if rec != nil || stack != "" {
		t.Fatalf("RunInvocation error path recovered/stack = (%v, %q), want empty", rec, stack)
	}
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestRunInvocation_Panic(t *testing.T) {
	inner := func(_ context.Context, _ *MiddlewareContext) error { panic("kaboom") }
	rec, stack, err := RunInvocation(context.Background(), &MiddlewareContext{}, nil, inner)
	if err != nil {
		t.Fatalf("panic path err = %v, want nil", err)
	}
	if rec != "kaboom" {
		t.Fatalf("recovered = %v, want kaboom", rec)
	}
	if !strings.Contains(stack, "RunInvocation") && stack == "" {
		t.Fatal("stack trace was not captured")
	}
}

func TestRunInvocation_ComposesMiddleware(t *testing.T) {
	app := FunctionApp()
	var order []string
	app.Use(MiddlewareFunc(func(next Handler) Handler {
		return func(ctx context.Context, mc *MiddlewareContext) error {
			order = append(order, "before")
			err := next(ctx, mc)
			order = append(order, "after")
			return err
		}
	}))
	inner := func(_ context.Context, _ *MiddlewareContext) error {
		order = append(order, "inner")
		return nil
	}

	if _, _, err := RunInvocation(context.Background(), &MiddlewareContext{}, app, inner); err != nil {
		t.Fatalf("RunInvocation err = %v", err)
	}
	if strings.Join(order, ",") != "before,inner,after" {
		t.Fatalf("order = %v, want before,inner,after", order)
	}
}
