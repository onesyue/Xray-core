package common_test

import (
	"context"
	"testing"

	. "github.com/xtls/xray-core/common"
)

type TConfig struct {
	value int
}

type YConfig struct {
	value string
}

type ZConfig struct {
	value string
}

func TestObjectCreation(t *testing.T) {
	f := func(ctx context.Context, t interface{}) (interface{}, error) {
		return func() int {
			return t.(*TConfig).value
		}, nil
	}

	Must(RegisterConfig((*TConfig)(nil), f))
	err := RegisterConfig((*TConfig)(nil), f)
	if err == nil {
		t.Error("expect non-nil error, but got nil")
	}

	g, err := CreateObject(context.Background(), &TConfig{value: 2})
	Must(err)
	if v := g.(func() int)(); v != 2 {
		t.Error("expect return value 2, but got ", v)
	}

	_, err = CreateObject(context.Background(), &YConfig{value: "T"})
	if err == nil {
		t.Error("expect non-nil error, but got nil")
	}
}

func TestReplaceConfigCreator(t *testing.T) {
	original := func(context.Context, interface{}) (interface{}, error) {
		return "original", nil
	}
	replacement := func(context.Context, interface{}) (interface{}, error) {
		return "replacement", nil
	}

	Must(RegisterConfig((*ZConfig)(nil), original))
	previous, err := ReplaceConfigCreator((*ZConfig)(nil), replacement)
	Must(err)
	defer func() {
		_, restoreErr := ReplaceConfigCreator((*ZConfig)(nil), previous)
		Must(restoreErr)
	}()

	created, err := CreateObject(context.Background(), &ZConfig{value: "ignored"})
	Must(err)
	if created != "replacement" {
		t.Fatalf("CreateObject() = %v, want replacement", created)
	}

	if _, err := ReplaceConfigCreator((*YConfig)(nil), replacement); err == nil {
		t.Fatal("ReplaceConfigCreator accepted an unregistered config")
	}
	if _, err := ReplaceConfigCreator((*ZConfig)(nil), nil); err == nil {
		t.Fatal("ReplaceConfigCreator accepted a nil replacement")
	}
}
