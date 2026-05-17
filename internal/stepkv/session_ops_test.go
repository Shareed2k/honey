package stepkv

import (
	"testing"
	"time"
)

func TestSession_GetPutDelete(t *testing.T) {
	s, err := Start(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Put("foo", "bar"); err != nil {
		t.Fatal(err)
	}
	val, found, err := s.Get("foo")
	if err != nil || !found || val != "bar" {
		t.Fatalf("get foo: val=%q found=%v err=%v", val, found, err)
	}
	if err := s.Delete("foo"); err != nil {
		t.Fatal(err)
	}
	_, found, err = s.Get("foo")
	if err != nil || found {
		t.Fatalf("after delete: found=%v err=%v", found, err)
	}
}

func TestSession_validateKey(t *testing.T) {
	s, err := Start(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	for _, key := range []string{"", "a/b", "__health", string(make([]byte, maxKeyLen+1))} {
		if err := s.Put(key, "x"); err == nil {
			t.Fatalf("Put(%q): want error", key)
		}
	}
	if err := s.Put("k", string(make([]byte, maxValueLen+1))); err != ErrValueTooLong {
		t.Fatalf("long value: got %v", err)
	}
}
