package file

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"parade/internal/core/db"
)

func TestGetLocalTree(t *testing.T) {
	root := t.TempDir()
	subDir := filepath.Join(root, "docs")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatalf("write child file failed: %v", err)
	}

	engine := NewEngine()
	if err := engine.ShareDirectory(root); err != nil {
		t.Fatalf("share directory failed: %v", err)
	}

	tree, err := engine.GetLocalTree()
	if err != nil {
		t.Fatalf("get local tree failed: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("expected 1 root node, got %d", len(tree))
	}
	if tree[0].Name == "" || !tree[0].IsFolder {
		t.Fatalf("root node invalid: %+v", tree[0])
	}
	if len(tree[0].Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(tree[0].Children))
	}
}

func TestGetChunk(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "big.bin")

	size := DefaultChunkSize + 10
	content := make([]byte, size)
	for index := range content {
		content[index] = byte(index % 251)
	}
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	engine := NewEngine()
	first, err := engine.GetChunk(filePath, 0)
	if err != nil {
		t.Fatalf("first chunk read failed: %v", err)
	}
	if len(first) != DefaultChunkSize {
		t.Fatalf("expected first chunk size %d, got %d", DefaultChunkSize, len(first))
	}

	second, err := engine.GetChunk(filePath, int64(DefaultChunkSize))
	if err != nil {
		t.Fatalf("second chunk read failed: %v", err)
	}
	if len(second) != 10 {
		t.Fatalf("expected second chunk size 10, got %d", len(second))
	}

	_, err = engine.GetChunk(filePath, int64(size))
	if err != io.EOF {
		t.Fatalf("expected EOF at file end, got %v", err)
	}
}

func TestPrepareDownloadAndSaveChunk(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "test.db")
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		t.Fatalf("init db failed: %v", err)
	}
	defer database.Close()

	engine := NewEngine().WithDatabase(database)
	ctx := context.Background()

	taskID := "task-1"
	targetPath := filepath.Join(root, "out.txt")
	totalSize := int64(11)

	offset, err := engine.PrepareDownload(ctx, taskID, targetPath, "peer-a", totalSize)
	if err != nil {
		t.Fatalf("prepare download failed: %v", err)
	}
	if offset != 0 {
		t.Fatalf("expected offset 0, got %d", offset)
	}

	if err := engine.SaveChunk(ctx, taskID, targetPath, "peer-a", []byte("hello "), 0, totalSize); err != nil {
		t.Fatalf("save first chunk failed: %v", err)
	}

	resumeOffset, err := engine.PrepareDownload(ctx, taskID, targetPath, "peer-a", totalSize)
	if err != nil {
		t.Fatalf("prepare download resume failed: %v", err)
	}
	if resumeOffset != 6 {
		t.Fatalf("expected resume offset 6, got %d", resumeOffset)
	}

	if err := engine.SaveChunk(ctx, taskID, targetPath, "peer-a", []byte("world"), 6, totalSize); err != nil {
		t.Fatalf("save second chunk failed: %v", err)
	}

	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read output failed: %v", err)
	}
	if string(raw) != "hello world" {
		t.Fatalf("unexpected output content: %q", string(raw))
	}

	log, err := database.GetFileLog(ctx, taskID)
	if err != nil {
		t.Fatalf("read file log failed: %v", err)
	}
	if log == nil {
		t.Fatal("file log should not be nil")
	}
	if log.Transferred != totalSize || log.Status != statusCompleted {
		t.Fatalf("unexpected log state: transferred=%d status=%d", log.Transferred, log.Status)
	}
}
