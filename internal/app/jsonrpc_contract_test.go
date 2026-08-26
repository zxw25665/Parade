package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterValidatesArguments(t *testing.T) {
	previous := registeredMethods
	registeredMethods = make(map[string]MethodHandler)
	t.Cleanup(func() { registeredMethods = previous })

	register("echo", func(value string) (string, error) {
		return value, nil
	})
	handler := registeredMethods["echo"]

	tests := []struct {
		name    string
		params  json.RawMessage
		want    string
		wantErr string
	}{
		{name: "missing arguments", wantErr: "echo expects 1 arguments, got 0"},
		{name: "invalid params JSON", params: json.RawMessage(`{"value":"parade"}`), wantErr: "invalid params for echo"},
		{name: "invalid argument JSON", params: json.RawMessage(`[42]`), wantErr: "invalid argument 0 for echo"},
		{name: "valid argument", params: json.RawMessage(`["parade"]`), want: "parade"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := handler(tt.params)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("handler error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("handler returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("handler result = %v, want %q", got, tt.want)
			}
		})
	}
}

func TestRegisterPropagatesSingleResultErrors(t *testing.T) {
	previous := registeredMethods
	registeredMethods = make(map[string]MethodHandler)
	t.Cleanup(func() { registeredMethods = previous })

	wantErr := errors.New("single result failure")
	register("fail", func() error { return wantErr })

	_, err := registeredMethods["fail"](nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("handler error = %v, want %v", err, wantErr)
	}
}

func TestRegisterPropagatesTwoResultErrors(t *testing.T) {
	previous := registeredMethods
	registeredMethods = make(map[string]MethodHandler)
	t.Cleanup(func() { registeredMethods = previous })

	wantErr := errors.New("lookup failure")
	register("lookup", func(string) (string, error) {
		return "", wantErr
	})

	_, err := registeredMethods["lookup"](json.RawMessage(`["parade"]`))
	if !errors.Is(err, wantErr) {
		t.Fatalf("handler error = %v, want %v", err, wantErr)
	}
}

func TestRegisterMethodsBuildsRealRegistry(t *testing.T) {
	previousMethods := registeredMethods
	t.Cleanup(func() {
		registeredMethods = previousMethods
	})

	identityPath := filepath.Join(t.TempDir(), "identity.json")
	app := NewApp(nil, nil, nil, nil, nil, nil, nil).WithIdentityPath(identityPath)
	RegisterMethods(app)

	if got := len(registeredMethods); got != 37 {
		t.Fatalf("registered method count = %d, want 37", got)
	}

	result, err := registeredMethods["CheckHasIdentity"](nil)
	if err != nil {
		t.Fatalf("CheckHasIdentity handler returned error: %v", err)
	}
	if result != false {
		t.Fatalf("CheckHasIdentity without identity = %v, want false", result)
	}

	if err := os.WriteFile(identityPath, []byte("identity"), 0600); err != nil {
		t.Fatalf("write identity fixture: %v", err)
	}
	result, err = registeredMethods["CheckHasIdentity"](nil)
	if err != nil {
		t.Fatalf("CheckHasIdentity existing identity returned error: %v", err)
	}
	if result != true {
		t.Fatalf("CheckHasIdentity with identity = %v, want true", result)
	}
}
