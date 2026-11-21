package temporal

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	sdklog "go.temporal.io/sdk/log"
	sdktemporal "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// dev is Temporal's dev server the tests run on, started once for the package.
var dev struct {
	address string
	c       client.Client
	skip    string // why there is none
}

// oneProcessNamespace is a namespace the dev server makes besides "default", for the README's
// example in testdata/examples (TestOneProcess): no other test's workers poll there.
const oneProcessNamespace = "one-process"

// quiet logs the SDK's warnings and errors only.
var quiet = sdklog.NewStructuredLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

func TestMain(m *testing.M) {
	// The tests wait for the tries again of an answer: they need not wait 2 seconds for each.
	answerRetry.InitialInterval = 100 * time.Millisecond
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	cli := os.Getenv("TEMPORAL_CLI")
	if cli == "" {
		// The file is named after the SDK's version, read from the SDK's constant: the build
		// information of a test binary does not give the versions of the modules it uses.
		cli = filepath.Join(os.TempDir(), "temporal-cli-go-sdk-"+sdktemporal.SDKVersion)
	}
	if _, err := os.Stat(cli); err != nil {
		dev.skip = fmt.Sprintf("no Temporal CLI at %s (TEMPORAL_CLI names one); the tests do not download it", cli)
		return m.Run()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var log tail
	ds, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{
		ExistingPath:  cli,
		LogLevel:      "error",
		ClientOptions: &client.Options{Logger: quiet},
		ExtraArgs:     []string{"--namespace", oneProcessNamespace},
		Stdout:        io.Discard,
		Stderr:        &log,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "starting Temporal's dev server: %v\n%s", err, log.String())
		return 1
	}
	defer ds.Stop()
	dev.address = ds.FrontendHostPort()
	dev.c = ds.Client()
	return m.Run()
}

// tail keeps the last of what is written to it: the dev server's log, for when it does not start.
type tail struct {
	mu sync.Mutex
	b  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 8192 {
		t.b = t.b[len(t.b)-8192:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// needServer skips a test when there is no dev server, and answers its address.
func needServer(t *testing.T) string {
	t.Helper()
	if dev.skip != "" {
		t.Skip("SKIP: " + dev.skip)
	}
	return dev.address
}
