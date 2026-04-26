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

	// PrepareDownload after completion should return ErrDownloadCompleted
	_, err = engine.PrepareDownload(ctx, taskID, targetPath, "peer-a", totalSize)
	if err != ErrDownloadCompleted {
		t.Fatalf("expected ErrDownloadCompleted, got %v", err)
	}
}

// ---- ChunkTracker 单元测试 ----

func TestChunkTrackerSingleSlot(t *testing.T) {
	ct := NewChunkTracker(100) // 100 bytes, 1 slot
	done, err := ct.MarkReceived(0, 100)
	if err != nil {
		t.Fatalf("MarkReceived failed: %v", err)
	}
	if !done {
		t.Fatal("expected complete after writing all 100 bytes")
	}
	if !ct.IsComplete() {
		t.Fatal("IsComplete should be true")
	}
	if len(ct.MissingOffsets()) != 0 {
		t.Fatal("expected no missing offsets")
	}
	if ct.BytesReceived() != 100 {
		t.Fatalf("expected BytesReceived=100, got %d", ct.BytesReceived())
	}
}

func TestChunkTrackerMultiSlot(t *testing.T) {
	sz := int64(DefaultChunkSize * 3)
	ct := NewChunkTracker(sz)

	done, err := ct.MarkReceived(0, DefaultChunkSize)
	if err != nil || done {
		t.Fatalf("unexpected: err=%v done=%v", err, done)
	}
	done, err = ct.MarkReceived(DefaultChunkSize*2, DefaultChunkSize)
	if err != nil || done {
		t.Fatalf("unexpected: err=%v done=%v", err, done)
	}
	if ct.IsComplete() {
		t.Fatal("should not be complete yet (slot 1 missing)")
	}

	done, err = ct.MarkReceived(DefaultChunkSize, DefaultChunkSize)
	if err != nil {
		t.Fatalf("MarkReceived failed: %v", err)
	}
	if !done {
		t.Fatal("expected complete after all 3 slots")
	}
}

func TestChunkTrackerOutOfOrder(t *testing.T) {
	sz := int64(DefaultChunkSize * 3)
	ct := NewChunkTracker(sz)

	// Last chunk arrives first
	done, err := ct.MarkReceived(DefaultChunkSize*2, DefaultChunkSize)
	if err != nil || done {
		t.Fatalf("unexpected: err=%v done=%v", err, done)
	}
	if ct.IsComplete() {
		t.Fatal("should not be complete when only last slot is marked")
	}
	if ct.BytesReceived() != DefaultChunkSize {
		t.Fatalf("expected BytesReceived=%d, got %d", DefaultChunkSize, ct.BytesReceived())
	}

	// First chunk arrives
	done, err = ct.MarkReceived(0, DefaultChunkSize)
	if err != nil || done {
		t.Fatalf("unexpected: err=%v done=%v", err, done)
	}

	// Middle chunk arrives — complete now
	done, err = ct.MarkReceived(DefaultChunkSize, DefaultChunkSize)
	if err != nil {
		t.Fatalf("MarkReceived failed: %v", err)
	}
	if !done {
		t.Fatal("expected complete after all 3 slots")
	}
}

func TestChunkTrackerDuplicateChunk(t *testing.T) {
	ct := NewChunkTracker(DefaultChunkSize)

	done, err := ct.MarkReceived(0, DefaultChunkSize)
	if err != nil || !done {
		t.Fatalf("unexpected: err=%v done=%v", err, done)
	}

	// Same chunk again — idempotent
	done, err = ct.MarkReceived(0, DefaultChunkSize)
	if err != nil || !done {
		t.Fatalf("duplicate should be idempotent: err=%v done=%v", err, done)
	}
	if ct.BytesReceived() != DefaultChunkSize {
		t.Fatalf("duplicate should not increase BytesReceived: %d", ct.BytesReceived())
	}
}

func TestChunkTrackerMissingOffsets(t *testing.T) {
	sz := int64(DefaultChunkSize * 4)
	ct := NewChunkTracker(sz)

	ct.MarkReceived(DefaultChunkSize, DefaultChunkSize)
	ct.MarkReceived(DefaultChunkSize*3, DefaultChunkSize)

	missing := ct.MissingOffsets()
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing offsets, got %d: %v", len(missing), missing)
	}
	if missing[0] != 0 {
		t.Fatalf("expected missing offset 0, got %d", missing[0])
	}
	if missing[1] != DefaultChunkSize*2 {
		t.Fatalf("expected missing offset %d, got %d", DefaultChunkSize*2, missing[1])
	}
}

func TestChunkTrackerSaveLoad(t *testing.T) {
	dir := t.TempDir()
	sz := int64(DefaultChunkSize * 5)
	ct := NewChunkTracker(sz)

	ct.MarkReceived(0, DefaultChunkSize)
	ct.MarkReceived(DefaultChunkSize*2, DefaultChunkSize)
	ct.MarkReceived(DefaultChunkSize*4, DefaultChunkSize)

	bitmapPath := filepath.Join(dir, "test.bitmap")
	if err := ct.Save(bitmapPath); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := LoadChunkTracker(bitmapPath, sz)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if ct.received != loaded.received {
		t.Fatalf("received count mismatch: %d vs %d", ct.received, loaded.received)
	}
	if ct.BytesReceived() != loaded.BytesReceived() {
		t.Fatalf("BytesReceived mismatch: %d vs %d", ct.BytesReceived(), loaded.BytesReceived())
	}
	if ct.IsComplete() != loaded.IsComplete() {
		t.Fatalf("IsComplete mismatch")
	}
	// Loading should preserve the bitmap — MissingOffsets should match
	origMissing := ct.MissingOffsets()
	loadedMissing := loaded.MissingOffsets()
	if len(origMissing) != len(loadedMissing) {
		t.Fatalf("MissingOffsets length mismatch: %d vs %d", len(origMissing), len(loadedMissing))
	}
	for i := range origMissing {
		if origMissing[i] != loadedMissing[i] {
			t.Fatalf("MissingOffsets[%d] mismatch: %d vs %d", i, origMissing[i], loadedMissing[i])
		}
	}
}

func TestChunkTrackerBoundary(t *testing.T) {
	// File size exactly at chunk boundary
	sz := int64(DefaultChunkSize * 2) // exactly 2 chunks
	ct := NewChunkTracker(sz)

	done, err := ct.MarkReceived(0, DefaultChunkSize)
	if err != nil || done {
		t.Fatalf("unexpected: err=%v done=%v", err, done)
	}
	done, err = ct.MarkReceived(DefaultChunkSize, DefaultChunkSize)
	if err != nil || !done {
		t.Fatalf("expected complete at boundary: err=%v done=%v", err, done)
	}
}

func TestChunkTrackerSmallFile(t *testing.T) {
	// File smaller than one chunk, received as two partial writes
	ct := NewChunkTracker(11)

	done, err := ct.MarkReceived(0, 6)
	if err != nil || done {
		t.Fatalf("unexpected: err=%v done=%v", err, done)
	}
	if ct.IsComplete() {
		t.Fatal("should not be complete after 6 of 11 bytes")
	}

	done, err = ct.MarkReceived(6, 5)
	if err != nil {
		t.Fatalf("MarkReceived failed: %v", err)
	}
	if !done {
		t.Fatal("expected complete after all 11 bytes")
	}
	if ct.BytesReceived() != 11 {
		t.Fatalf("expected BytesReceived=11, got %d", ct.BytesReceived())
	}
}

// ---- SaveChunk 集成测试 ----

func TestSaveChunkOutOfOrder(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "test.db")
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		t.Fatalf("init db failed: %v", err)
	}
	defer database.Close()

	engine := NewEngine().WithDatabase(database)
	ctx := context.Background()
	taskID := "out-of-order-task"
	targetPath := filepath.Join(root, "output.bin")
	totalSize := int64(DefaultChunkSize*3 + 100)

	// Chunks arrive in reverse order
	chunks := []struct {
		offset int64
		data   []byte
	}{
		{DefaultChunkSize * 2, make([]byte, DefaultChunkSize+100)},  // last chunk (partial)
		{DefaultChunkSize, make([]byte, DefaultChunkSize)},          // middle chunk
		{0, make([]byte, DefaultChunkSize)},                         // first chunk
	}

	for i, c := range chunks {
		err := engine.SaveChunk(ctx, taskID, targetPath, "peer-a", c.data, c.offset, totalSize)
		if err != nil {
			t.Fatalf("chunk %d (offset %d) failed: %v", i, c.offset, err)
		}
	}

	// Verify file exists and has correct size
	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("stat output file failed: %v", err)
	}
	if info.Size() != totalSize {
		t.Fatalf("expected file size %d, got %d", totalSize, info.Size())
	}

	// Verify DB log is completed
	log, err := database.GetFileLog(ctx, taskID)
	if err != nil {
		t.Fatalf("get file log failed: %v", err)
	}
	if log == nil || log.Status != statusCompleted {
		t.Fatalf("expected completed status, got %+v", log)
	}
}

func TestSaveChunkIncompleteShouldNotFinalize(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "test.db")
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		t.Fatalf("init db failed: %v", err)
	}
	defer database.Close()

	engine := NewEngine().WithDatabase(database)
	ctx := context.Background()
	taskID := "partial-task"
	targetPath := filepath.Join(root, "partial.bin")
	totalSize := int64(DefaultChunkSize * 4)

	// Send only 2 of 4 chunks
	err = engine.SaveChunk(ctx, taskID, targetPath, "peer-a",
		make([]byte, DefaultChunkSize), 0, totalSize)
	if err != nil {
		t.Fatalf("save chunk 0 failed: %v", err)
	}
	err = engine.SaveChunk(ctx, taskID, targetPath, "peer-a",
		make([]byte, DefaultChunkSize), DefaultChunkSize*3, totalSize)
	if err != nil {
		t.Fatalf("save chunk 3 failed: %v", err)
	}

	// File should NOT exist at final path (still .parade_tmp only)
	if _, err := os.Stat(targetPath); err == nil {
		t.Fatal("file should NOT be finalized (incomplete)")
	}

	// Temp file should exist
	tmpPath := targetPath + ".parade_tmp"
	if _, err := os.Stat(tmpPath); os.IsNotExist(err) {
		t.Fatal("temp file should exist")
	}

	// DB log should be transferring
	log, err := database.GetFileLog(ctx, taskID)
	if err != nil {
		t.Fatalf("get file log failed: %v", err)
	}
	if log == nil || log.Status != statusTransferring {
		t.Fatalf("expected transferring status, got %+v", log)
	}
}

func TestSaveChunkDupChunkIdempotent(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "test.db")
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		t.Fatalf("init db failed: %v", err)
	}
	defer database.Close()

	engine := NewEngine().WithDatabase(database)
	ctx := context.Background()
	taskID := "dup-task"
	targetPath := filepath.Join(root, "dup.bin")
	data := []byte("hello world")
	totalSize := int64(len(data))

	// Send same chunk twice
	err = engine.SaveChunk(ctx, taskID, targetPath, "peer-a", data, 0, totalSize)
	if err != nil {
		t.Fatalf("first save failed: %v", err)
	}
	err = engine.SaveChunk(ctx, taskID, targetPath, "peer-a", data, 0, totalSize)
	if err != nil {
		t.Fatalf("duplicate save failed: %v", err)
	}

	// File should be finalized once
	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read output failed: %v", err)
	}
	if string(raw) != "hello world" {
		t.Fatalf("unexpected content: %q", string(raw))
	}
}
