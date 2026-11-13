package consumer

import (
	"strings"
	"testing"
)

func TestBusinessConsumerExcludesLiveCredentials(t *testing.T) {
	got := environment([]string{"FRAME_BUSINESS_TEST_POSTGRES_DSN=secret", "BUSINESS_AUTH_ENCRYPTION_KEY=secret", "ENT_AUTH_ENCRYPTION_KEY=secret"}, Options{}, "")
	if strings.Contains(strings.Join(got, "\n"), "secret") {
		t.Fatal("consumer inherited live runtime credentials")
	}
}
