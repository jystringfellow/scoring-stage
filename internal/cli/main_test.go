package cli

import (
	"os"
	"strconv"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("STAGE_TEST_AS_STAGE") == "1" {
		if limit := os.Getenv("STAGE_TEST_JOB_OUTPUT_LIMIT"); limit != "" {
			jobOutputLimit, _ = strconv.ParseInt(limit, 10, 64)
		}
		os.Exit(New().Main(os.Args[1:]))
	}
	os.Exit(m.Run())
}
