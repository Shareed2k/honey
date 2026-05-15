package hosts

import "testing"

func TestMetaSSHPort(t *testing.T) {
	t.Parallel()
	if _, ok := MetaSSHPort(nil); ok {
		t.Fatal("nil record")
	}
	if _, ok := MetaSSHPort(&Record{}); ok {
		t.Fatal("empty meta")
	}
	if _, ok := MetaSSHPort(&Record{Meta: map[string]string{"ssh_port": ""}}); ok {
		t.Fatal("empty value")
	}
	if _, ok := MetaSSHPort(&Record{Meta: map[string]string{"ssh_port": "0"}}); ok {
		t.Fatal("zero")
	}
	if _, ok := MetaSSHPort(&Record{Meta: map[string]string{"ssh_port": "65536"}}); ok {
		t.Fatal("too large")
	}
	p, ok := MetaSSHPort(&Record{Meta: map[string]string{"ssh_port": " 2222 "}})
	if !ok || p != 2222 {
		t.Fatalf("got %d ok=%v", p, ok)
	}
}

func TestCloneWithMetaSSHPort(t *testing.T) {
	t.Parallel()
	r := Record{Name: "a", Meta: map[string]string{"x": "y"}}
	c := CloneWithMetaSSHPort(r, 2222)
	if c.Meta["x"] != "y" || c.Meta["ssh_port"] != "2222" {
		t.Fatalf("meta=%v", c.Meta)
	}
	if r.Meta["ssh_port"] != "" {
		t.Fatal("mutated original")
	}
}
