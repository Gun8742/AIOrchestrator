package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pixel-agent-office/backend/internal/testdb"
)

func TestConfiguredOAuthProviderInRunningApp(t *testing.T) {
	if os.Getenv("APP_RUNTIME_SMOKE") != "1" {
		t.Skip("set APP_RUNTIME_SMOKE=1 to start the local application")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Skip("port 8080 is in use")
	}
	listener.Close()
	binary := filepath.Join(t.TempDir(), "pixel-backend")
	if err = exec.Command("go", "build", "-o", binary, ".").Run(); err != nil {
		t.Fatal("cannot build application")
	}
	t.Setenv("DATABASE_URL", testdb.URL(t))
	frontend, err := filepath.Abs("../../../frontend/dist")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FRONTEND_DIR", frontend)
	cmd := exec.Command(binary)
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	secrets := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			const prefix = "Local bootstrap secret (expires in 10 minutes): "
			if secret, ok := strings.CutPrefix(scanner.Text(), prefix); ok {
				secrets <- secret
				return
			}
		}
	}()
	var secret string
	select {
	case secret = <-secrets:
	case <-time.After(20 * time.Second):
		t.Fatal("application did not become ready")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	body, err := json.Marshal(map[string]string{"secret": secret})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest("POST", "http://127.0.0.1:8080/api/bootstrap", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("bootstrap request failed")
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("bootstrap rejected")
	}
	response, err = client.Get("http://127.0.0.1:8080/api/providers")
	if err != nil {
		t.Fatal("provider request failed")
	}
	defer response.Body.Close()
	var providers []struct {
		ID           string          `json:"id"`
		Available    bool            `json:"available"`
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err = json.NewDecoder(response.Body).Decode(&providers); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, provider := range providers {
		if provider.ID == "antigravity" {
			found = true
			if !provider.Available || !provider.Capabilities["cancellation"] || provider.Capabilities["approval"] {
				t.Fatal("incorrect production OAuth capabilities")
			}
		}
	}
	if !found {
		t.Fatal("OAuth provider missing")
	}
	response, err = client.Get("http://127.0.0.1:8080/")
	if err != nil {
		t.Fatal("frontend request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("frontend unavailable or missing security policy")
	}
}
