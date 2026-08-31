package strmutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsDeletedBak(t *testing.T) {
	tests := []struct {
		name string
		p    string
		want bool
	}{
		{"bak", "/data/movie.deleted.bak", true},
		{"strm", "/data/movie.strm", false},
		{"empty", "", false},
		{"dir", "/data/movie", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDeletedBak(tt.p); got != tt.want {
				t.Errorf("IsDeletedBak(%q) = %v, want %v", tt.p, got, tt.want)
			}
		})
	}
}

func TestDeleteStrmFile_NotExist(t *testing.T) {
	if err := DeleteStrmFile("/nonexistent/path/file.strm"); err != nil {
		t.Errorf("DeleteStrmFile on nonexistent path should return nil, got %v", err)
	}
}

func TestDeleteStrmFile_Exist(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.strm")
	if err := os.WriteFile(p, []byte("http://test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := DeleteStrmFile(p); err != nil {
		t.Errorf("DeleteStrmFile on existing file should return nil, got %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestDeletePath_NotExist(t *testing.T) {
	if err := DeletePath("/nonexistent/path"); err != nil {
		t.Errorf("DeletePath on nonexistent path should return nil, got %v", err)
	}
}

func TestDeletePath_Dir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := DeletePath(sub); err != nil {
		t.Errorf("DeletePath on existing dir should return nil, got %v", err)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Error("dir should be deleted")
	}
}

func TestExtractPickcode_Found(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.strm")
	content := "http://127.0.0.1:8090/api/fs/get?account=test&pickcode=SW8R7KcJ3qert5Zm9&file_name=movie.mp4"
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ExtractPickcode(p)
	if err != nil {
		t.Fatalf("ExtractPickcode error: %v", err)
	}
	if got != "SW8R7KcJ3qert5Zm9" {
		t.Errorf("ExtractPickcode = %q, want %q", got, "SW8R7KcJ3qert5Zm9")
	}
}

func TestExtractPickcode_NotFound(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.strm")
	if err := os.WriteFile(p, []byte("http://example.com/movie.mp4"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ExtractPickcode(p)
	if err != nil {
		t.Fatalf("ExtractPickcode error: %v", err)
	}
	if got != "" {
		t.Errorf("ExtractPickcode on no-pickcode content = %q, want empty", got)
	}
}

func TestExtractPickcode_NotExistFile(t *testing.T) {
	_, err := ExtractPickcode("/nonexistent/file.strm")
	if err == nil {
		t.Error("ExtractPickcode on nonexistent file should return error")
	}
}
