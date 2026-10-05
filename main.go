// Reproduces the stale-target stop scenario from NVIDIA/OpenShell#4009 using
// only the public Go SDK.
//
// A controller observes execution A of a sandbox and later asks the Gateway to
// stop it. In between, the sandbox was replaced (stop/start, or delete and
// recreate under the same name). The public Stop API takes only workspace and
// name, so the controller cannot say "stop only if A is still current", and the
// replacement execution is stopped.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/types"
)

const ws = "default"

// execution is everything the public API exposes to identify a run.
type execution struct {
	id      string
	started int64 // Status.MainProcessStartedAtMs
}

func (e execution) String() string {
	return fmt.Sprintf("sandbox_id=%s main_started_ms=%d", e.id, e.started)
}

func observe(s *v1.Sandbox) execution {
	return execution{id: s.ID, started: s.Status.MainProcessStartedAtMs}
}

func main() {
	address := flag.String("address", "localhost:8080", "Gateway endpoint")
	tlsDir := flag.String("tls-dir", "", "Directory with ca.crt, tls.crt, tls.key (omit for plaintext)")
	image := flag.String("image", "", "Sandbox image the Gateway can run (empty = Gateway default)")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cfg := v1.Config{Address: *address}
	if *tlsDir != "" {
		cfg.TLS = &types.TLSConfig{CAFile: filepath.Join(*tlsDir, "ca.crt"), CertFile: filepath.Join(*tlsDir, "tls.crt"), KeyFile: filepath.Join(*tlsDir, "tls.key")}
	}
	c, err := v1.NewClient(cfg)
	must(err)
	defer c.Close()

	spec := &v1.SandboxSpec{Command: []string{"/bin/sh", "-c", "sleep 600"}}
	if *image != "" {
		spec.Template = &v1.SandboxTemplate{Image: *image}
	}
	stamp := time.Now().Unix()
	bad := 0
	for _, sc := range []struct {
		label   string
		replace func(ctx context.Context, c *v1.Client, name string) *v1.Sandbox
	}{
		{"stop/start", restart},
		{"delete/recreate", func(ctx context.Context, c *v1.Client, name string) *v1.Sandbox { return recreate(ctx, c, name, spec) }},
	} {
		name := fmt.Sprintf("stp-%s-%d", sc.label[:2], stamp%100000000)
		fmt.Printf("== scenario: %s (sandbox %s)\n", sc.label, name)
		_, err := c.Sandboxes().Create(ctx, ws, name, spec, nil)
		must(err)
		s, err := c.Sandboxes().WaitReady(ctx, ws, name)
		must(err)
		a := observe(s)
		fmt.Printf("execution A observed: %s\n", a)

		b := observe(sc.replace(ctx, c, name))
		fmt.Printf("execution B (replacement) ready: %s\n", b)
		fmt.Printf("  same sandbox id: %t, same start time: %t\n", a.id == b.id, a.started == b.started)

		// The delayed controller action. Stop accepts only workspace and name;
		// there is no field to bind the request to execution A.
		fmt.Println("delayed stop for A: Sandboxes().Stop(workspace, name)")
		_, err = c.Sandboxes().Stop(ctx, ws, name)
		fmt.Printf("  stop result: err=%v\n", err)
		wctx, wc := context.WithTimeout(ctx, 60*time.Second)
		after, werr := c.Sandboxes().WaitStopped(wctx, ws, name)
		wc()
		stopped := werr == nil
		if stopped {
			fmt.Printf("  replacement B was STOPPED (phase=%v)\n", after.Status.Phase)
			bad++
		} else {
			fmt.Printf("  replacement B still running: %v\n", werr)
		}
		_, _ = c.Sandboxes().Delete(context.Background(), ws, name)
	}
	if bad > 0 {
		fmt.Printf("RESULT: STALE STOP AFFECTED THE REPLACEMENT in %d/2 scenarios; the API cannot express a target-execution precondition\n", bad)
		os.Exit(2)
	}
	fmt.Println("RESULT: replacement was not stopped")
}

func restart(ctx context.Context, c *v1.Client, name string) *v1.Sandbox {
	_, err := c.Sandboxes().Stop(ctx, ws, name)
	must(err)
	_, err = c.Sandboxes().WaitStopped(ctx, ws, name)
	must(err)
	_, err = c.Sandboxes().Start(ctx, ws, name)
	must(err)
	s, err := c.Sandboxes().WaitReady(ctx, ws, name)
	must(err)
	return s
}

func recreate(ctx context.Context, c *v1.Client, name string, spec *v1.SandboxSpec) *v1.Sandbox {
	_, err := c.Sandboxes().Delete(ctx, ws, name)
	must(err)
	for {
		if _, err := c.Sandboxes().Get(ctx, ws, name); v1.IsNotFound(err) {
			break
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			must(ctx.Err())
		}
	}
	_, err = c.Sandboxes().Create(ctx, ws, name, spec, nil)
	must(err)
	s, err := c.Sandboxes().WaitReady(ctx, ws, name)
	must(err)
	return s
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
