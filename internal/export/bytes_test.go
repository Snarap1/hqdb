package export

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Snarap1/hqdb/internal/fakesql"
	"github.com/Snarap1/hqdb/internal/values"
)

// TestQueryOutputBytesAreStable pins the exact bytes Query writes for every
// format on a fixed dataset, so buffering or other internal changes cannot
// alter the output. The dataset holds every value kind the fake driver
// serves (CSV quoting, JSON escaping, times, bytes, nulls) and is large
// enough to cross many write-buffer boundaries. The hashes were taken from
// the unbuffered writers; changing the output format on purpose means
// updating them in the same change.
func TestQueryOutputBytesAreStable(t *testing.T) {
	cases := []struct {
		format string
		limit  int
		size   int
		sha256 string
	}{
		{"csv", 0, 346467, "aea3b735da499d03be6be70b043218ac4a68793555eb9d927e4e2b749dc89c05"},
		{"json", 0, 702888, "f314c3ec827965998b6ba579bcc31cc74a436a67679049275d9435299440658d"},
		{"ndjson", 0, 699886, "17da33dbd6ffb9c915c3f9d09c24203ab3dff89df670c57e3ad3514b0c78a195"},
		{"csv", 7, 839, "1ff03c7b6e59933aec90b827c946d842cfa256c380fdfb15cd89bf6be592a221"},
		{"json", 7, 1599, "fe621410eacc9c01f749b75fc0e18f9c86643033af5febc8867817ac94025e26"},
		{"ndjson", 7, 1590, "af6f760c500a59f53a8be6cb6e37dd064f69b12e4fa29b95f3141490ca5e9fbd"},
	}
	db, _ := fakesql.Open(3000, 9)
	t.Cleanup(func() { db.Close() })
	dir := t.TempDir()
	for _, c := range cases {
		dest := filepath.Join(dir, "out."+c.format)
		if _, err := Query(context.Background(), db, "SELECT", dest, c.format, c.limit, nil); err != nil {
			t.Fatalf("%s limit %d: %v", c.format, c.limit, err)
		}
		data, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != c.sha256 || len(data) != c.size {
			t.Errorf("%s limit %d: %d bytes sha256 %s, want %d bytes sha256 %s",
				c.format, c.limit, len(data), got, c.size, c.sha256)
		}
	}
}

// fixedDocuments is a DocumentSource of n Documents with nested values,
// built from the fake driver's values.
func fixedDocuments(n int) DocumentSource {
	return func(emit func(any) error) error {
		for i := 0; i < n; i++ {
			d := values.Doc{{Key: "_id", Value: values.Doc{{Key: "$oid", Value: fmt.Sprintf("65a%05d", i)}}}}
			for c := 1; c < 8; c++ {
				d = append(d, values.Field{Key: fmt.Sprintf("f%d", c), Value: values.JSON(fakesql.Value(i, c))})
			}
			d = append(d, values.Field{Key: "nested", Value: values.Doc{
				{Key: "tags", Value: []any{values.JSON(fakesql.Value(i, 1)), int64(i)}},
			}})
			if err := emit(d); err != nil {
				return err
			}
		}
		return nil
	}
}

// TestDocumentsFlushesWhatWasWrittenOnFailure checks the error path of the
// write buffer: a source that fails after many Documents leaves every
// Document written before the failure on disk, as unbuffered writes did.
func TestDocumentsFlushesWhatWasWrittenOnFailure(t *testing.T) {
	boom := errors.New("cursor died")
	dir := t.TempDir()
	for _, format := range []string{"json", "ndjson"} {
		whole := filepath.Join(dir, "whole."+format)
		if _, err := Documents(whole, format, fixedDocuments(2000)); err != nil {
			t.Fatal(err)
		}
		failed := filepath.Join(dir, "failed."+format)
		n, err := Documents(failed, format, func(emit func(any) error) error {
			if err := fixedDocuments(2000)(emit); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) || n != 2000 {
			t.Fatalf("%s: n = %d, err = %v; want 2000 and %v", format, n, err, boom)
		}
		want, _ := os.ReadFile(whole)
		if format == "json" {
			want = bytes.TrimSuffix(want, []byte("]\n"))
		}
		got, _ := os.ReadFile(failed)
		if !bytes.Equal(got, want) {
			t.Errorf("%s: failed export has %d bytes, want the %d bytes written before the failure", format, len(got), len(want))
		}
	}
}

// TestDocumentsOutputBytesAreStable is the Documents counterpart of
// TestQueryOutputBytesAreStable.
func TestDocumentsOutputBytesAreStable(t *testing.T) {
	cases := []struct {
		format string
		size   int
		sha256 string
	}{
		{"csv", 487668, "1e31590ecfe6dee37ac011c9103121b14f2291e0ddd4c15947080e9d3bf58dd7"},
		{"json", 648888, "ad16dfa2333511c546023431477a8c03205b7e31a9d72929fd14d3ec9c2c3a43"},
		{"ndjson", 645886, "72cebc07cb2c9a73041266dc1118143a59dff96b1004a1e453341615f1a0823b"},
	}
	dir := t.TempDir()
	for _, c := range cases {
		dest := filepath.Join(dir, "docs."+c.format)
		if _, err := Documents(dest, c.format, fixedDocuments(3000)); err != nil {
			t.Fatalf("%s: %v", c.format, err)
		}
		data, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != c.sha256 || len(data) != c.size {
			t.Errorf("%s: %d bytes sha256 %s, want %d bytes sha256 %s",
				c.format, len(data), got, c.size, c.sha256)
		}
	}
}
