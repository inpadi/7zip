package security

import (
	"bytes"
	"io"
	"testing"
)

func TestBudgetAppliesCompressionRatioLimit(t *testing.T) {
	var budget Budget
	budget.SetCompressedBytes(1)
	payload := bytes.Repeat([]byte{'x'}, int(CompressionRatioSlack)+MaxCompressionRatio+2)
	if _, err := budget.Copy(io.Discard, bytes.NewReader(payload), "bomb"); err == nil {
		t.Fatal("expected expanded-data budget error")
	}
}

func TestCheckCompressionRatioAllowsSlack(t *testing.T) {
	if err := CheckCompressionRatio("small", 2<<20, 2<<10); err != nil {
		t.Fatal(err)
	}
	if err := CheckCompressionRatio("bomb", 4<<20, 1<<10); err == nil {
		t.Fatal("expected compression ratio error")
	}
}
