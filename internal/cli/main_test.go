package cli

import (
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestMain(m *testing.M) {
	if os.Getenv("STAGE_TEST_AS_STAGE") == "1" {
		if limit := os.Getenv("STAGE_TEST_JOB_OUTPUT_LIMIT"); limit != "" {
			jobOutputLimit, _ = strconv.ParseInt(limit, 10, 64)
		}
		if dir := os.Getenv("STAGE_TEST_JOB_TMP"); dir != "" {
			jobSandboxTmp = dir
		}
		if grace := os.Getenv("STAGE_TEST_JOB_STOP_GRACE"); grace != "" {
			jobStopGrace, _ = time.ParseDuration(grace)
		}
		if confirm := os.Getenv("STAGE_TEST_JOB_STOP_CONFIRM"); confirm != "" {
			jobStopConfirm, _ = time.ParseDuration(confirm)
		}
		if wait := os.Getenv("STAGE_TEST_JOB_START_WAIT"); wait != "" {
			jobStartWait, _ = time.ParseDuration(wait)
		}
		if timeout := os.Getenv("STAGE_TEST_JOB_SIGNAL_TIMEOUT"); timeout != "" {
			jobSignalTimeout, _ = time.ParseDuration(timeout)
		}
		app := New()
		accepting := &testutil.HandlerTransport{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})}
		app.GitHubAPI = config.GitHubAPI{BaseURL: config.GitHubAPIURL, Client: accepting.Client()}
		os.Exit(app.Main(os.Args[1:]))
	}
	os.Exit(m.Run())
}
