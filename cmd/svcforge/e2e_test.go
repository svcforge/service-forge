package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGeneratedProjectEndToEnd(t *testing.T) {
	if os.Getenv("SVCFORGE_TEST_E2E") != "1" {
		t.Skip("set SVCFORGE_TEST_E2E=1 with buf and local protobuf plugins on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	run := func(dir, name string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	cli := filepath.Join(temp, "svcforge")
	run(root, "go", "build", "-o", cli, "./cmd/svcforge")
	run(temp, cli, "new", "demo", "--replace", root)
	project := filepath.Join(temp, "demo")
	run(project, cli, "doctor")
	bufConfig := filepath.Join(project, "buf.gen.yaml")
	rewriteFile(t, bufConfig, strings.NewReplacer(
		"remote: buf.build/protocolbuffers/go", "local: protoc-gen-go",
		"remote: buf.build/grpc/go", "local: protoc-gen-go-grpc",
	))
	run(project, cli, "proto", "gen")
	run(project, cli, "add", "service", "order-service")
	run(project, cli, "proto", "gen")
	run(project, "go", "mod", "tidy")
	run(project, "go", "build", "./...")

	gatewayPort, servicePort := twoLoopbackPorts(t)
	rewriteFile(t, filepath.Join(project, "config", "base.yaml"), strings.NewReplacer(
		"listen_ip: 0.0.0.0", "listen_ip: 127.0.0.1",
		"port: 8080", "port: "+gatewayPort,
		"port: 9000", "port: "+servicePort,
		"127.0.0.1:9000", "127.0.0.1:"+servicePort,
	))
	for _, service := range []struct{ name, pkg string }{
		{"service", "./services/example-service/cmd"},
		{"gateway", "./gateway/cmd"},
	} {
		binary := filepath.Join(temp, service.name)
		run(project, "go", "build", "-o", binary, service.pkg)
		startProcess(t, project, binary)
	}

	type todoResponse struct {
		Code string
		Data struct {
			Todos []struct{ Title string }
		}
	}
	client := &http.Client{Timeout: time.Second}
	endpoint := "http://127.0.0.1:" + gatewayPort + "/api/v1/todos"
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := client.Get(endpoint)
		if err == nil {
			var result todoResponse
			decodeErr := json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				if decodeErr != nil || result.Code != "OK" || len(result.Data.Todos) == 0 || result.Data.Todos[0].Title == "" {
					t.Fatalf("invalid Todo response: %+v (%v)", result, decodeErr)
				}
				break
			}
			err = errors.New(resp.Status)
		}
		if time.Now().After(deadline) {
			t.Fatalf("generated gateway did not return Todo data: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	type todo struct {
		ID    string
		Title string
		Done  bool
	}
	request := func(method, target, body string, status int) todo {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var envelope struct {
			Code string
			Data struct{ Todo todo }
		}
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			t.Fatalf("%s %s: %v", method, target, err)
		}
		if resp.StatusCode != status || (status == http.StatusOK && envelope.Code != "OK") {
			t.Fatalf("%s %s: status %d, code %s", method, target, resp.StatusCode, envelope.Code)
		}
		return envelope.Data.Todo
	}

	created := request(http.MethodPost, endpoint, `{"title":"integration todo"}`, http.StatusOK)
	if created.ID == "" || created.Title != "integration todo" {
		t.Fatalf("created Todo = %+v", created)
	}
	detail := endpoint + "/detail?id=" + url.QueryEscape(created.ID)
	if got := request(http.MethodGet, detail, "", http.StatusOK); got != created {
		t.Fatalf("retrieved Todo = %+v, want %+v", got, created)
	}
	id := strconv.Quote(created.ID)
	updated := request(http.MethodPut, endpoint, `{"id":`+id+`,"done":true}`, http.StatusOK)
	if updated.ID != created.ID || updated.Title != created.Title || !updated.Done {
		t.Fatalf("updated Todo = %+v", updated)
	}
	request(http.MethodDelete, endpoint, `{"id":`+id+`}`, http.StatusOK)
	request(http.MethodGet, detail, "", http.StatusNotFound)
}

func rewriteFile(t *testing.T, path string, replacements *strings.Replacer) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(replacements.Replace(string(data))), 0600); err != nil {
		t.Fatal(err)
	}
}

func twoLoopbackPorts(t *testing.T) (string, string) {
	t.Helper()
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	return strconv.Itoa(first.Addr().(*net.TCPAddr).Port), strconv.Itoa(second.Addr().(*net.TCPAddr).Port)
}

func startProcess(t *testing.T, dir, name string) {
	t.Helper()
	cmd := exec.Command(name)
	cmd.Dir = dir
	log, err := os.Create(name + ".log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Errorf("stop %s: %v", name, err)
		}
		select {
		case err := <-done:
			if err != nil || t.Failed() {
				data, _ := os.ReadFile(name + ".log")
				t.Logf("%s exit: %v\n%s", name, err, data)
			}
			if err != nil {
				t.Fail()
			}
		case <-time.After(20 * time.Second):
			cmd.Process.Kill()
			<-done
			t.Errorf("%s did not shut down", name)
		}
	})
}
