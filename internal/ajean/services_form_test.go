package ajean

import "testing"

func TestDeriveFromCommand(t *testing.T) {
	var s Service
	s.deriveFromCommand("llama-server -m x.gguf --port 8091 --api-key sk-1", "")
	if s.URL != "http://127.0.0.1:8091/v1" || s.APIKey != "sk-1" {
		t.Fatalf("%+v", s)
	}
	var b Service
	b.deriveFromCommand("/x/basalt serve --config c.json", "")
	if b.URL != "http://127.0.0.1:8080/v1" {
		t.Fatalf("défaut moteur : %q", b.URL)
	}
	c := Service{Exec: "/bin/llama server", Args: []string{"-m", "a b.gguf", "--port", "9000"}}
	if got := c.commandLine(); got != `'/bin/llama server' -m 'a b.gguf' --port 9000` {
		t.Fatalf("commandLine : %s", got)
	}
}
