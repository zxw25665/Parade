package network

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

func TestBrowseEntry_JSONRoundTrip(t *testing.T) {
	entry := &BrowseEntry{
		Name:        "test.txt",
		Path:        "/shared/test.txt",
		IsDirectory: false,
		Size:        1024,
		Hash:        "abc123",
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded BrowseEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Name != entry.Name {
		t.Errorf("Name: got %q, want %q", decoded.Name, entry.Name)
	}
	if decoded.Path != entry.Path {
		t.Errorf("Path: got %q, want %q", decoded.Path, entry.Path)
	}
	if decoded.IsDirectory != entry.IsDirectory {
		t.Errorf("IsDirectory: got %v, want %v", decoded.IsDirectory, entry.IsDirectory)
	}
	if decoded.Size != entry.Size {
		t.Errorf("Size: got %d, want %d", decoded.Size, entry.Size)
	}
	if decoded.Hash != entry.Hash {
		t.Errorf("Hash: got %q, want %q", decoded.Hash, entry.Hash)
	}
}

func TestBrowseEntry_SliceJSONRoundTrip(t *testing.T) {
	entries := []*BrowseEntry{
		{Name: "dir1", Path: "/shared/dir1", IsDirectory: true, Size: 0},
		{Name: "file1.txt", Path: "/shared/dir1/file1.txt", IsDirectory: false, Size: 2048, Hash: "def456"},
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal slice: %v", err)
	}
	var decoded []*BrowseEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal slice: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(decoded))
	}
	if decoded[0].Name != "dir1" {
		t.Errorf("entry[0].Name: got %q", decoded[0].Name)
	}
	if !decoded[0].IsDirectory {
		t.Error("entry[0] should be directory")
	}
}

func TestFileChunk_JSONRoundTrip(t *testing.T) {
	chunk := &FileChunk{
		TaskId:    "task-123",
		PeerId:    "peer-456",
		FilePath:  "/shared/file.bin",
		Offset:    0,
		Data:      []byte("hello world"),
		TotalSize: 1024,
		Eof:       false,
		FileHash:  "hash789",
	}
	data, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded FileChunk
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.TaskId != chunk.TaskId {
		t.Errorf("TaskId: got %q, want %q", decoded.TaskId, chunk.TaskId)
	}
	if decoded.Offset != chunk.Offset {
		t.Errorf("Offset: got %d, want %d", decoded.Offset, chunk.Offset)
	}
	if string(decoded.Data) != string(chunk.Data) {
		t.Errorf("Data: got %q, want %q", string(decoded.Data), string(chunk.Data))
	}
	if decoded.Eof != chunk.Eof {
		t.Errorf("Eof: got %v, want %v", decoded.Eof, chunk.Eof)
	}
}

func TestFileChunk_EOFRoundTrip(t *testing.T) {
	chunk := &FileChunk{
		TaskId:    "task-final",
		PeerId:    "peer-xyz",
		FilePath:  "/shared/file.bin",
		Offset:    4096,
		TotalSize: 4096,
		Eof:       true,
	}
	data, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("marshal EOF chunk: %v", err)
	}
	var decoded FileChunk
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal EOF chunk: %v", err)
	}
	if !decoded.Eof {
		t.Error("expected Eof=true")
	}
	if decoded.Data != nil {
		t.Errorf("expected nil Data for EOF chunk, got %v", decoded.Data)
	}
}

func TestPeerInfo_Basic(t *testing.T) {
	pi := PeerInfo{
		PeerUUID:  "uuid-abc",
		IPAddress: "192.168.1.100",
	}
	if pi.PeerUUID != "uuid-abc" {
		t.Errorf("PeerUUID: got %q", pi.PeerUUID)
	}
	if pi.IPAddress != "192.168.1.100" {
		t.Errorf("IPAddress: got %q", pi.IPAddress)
	}
}

func TestPeerStatus_Values(t *testing.T) {
	if PeerStatusOnline != "online" {
		t.Errorf("PeerStatusOnline: got %q", PeerStatusOnline)
	}
	if PeerStatusOffline != "offline" {
		t.Errorf("PeerStatusOffline: got %q", PeerStatusOffline)
	}
}

func TestPhaseResult_JSONRoundTrip(t *testing.T) {
	pr := PhaseResult{Success: true, Label: "正常", Error: ""}
	data, err := json.Marshal(pr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded PhaseResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Success != true {
		t.Error("expected Success=true")
	}
	if decoded.Label != "正常" {
		t.Errorf("Label: got %q", decoded.Label)
	}
}

func TestPeerConnectResult_Defaults(t *testing.T) {
	r := &PeerConnectResult{}
	if r.IP != "" {
		t.Errorf("expected empty IP, got %q", r.IP)
	}
	if r.Phase1.Success {
		t.Error("expected Phase1.Success=false")
	}
	if r.Phase2.Success {
		t.Error("expected Phase2.Success=false")
	}
	if r.Phase3Send.Success {
		t.Error("expected Phase3Send.Success=false")
	}
	if r.Phase3Recv.Success {
		t.Error("expected Phase3Recv.Success=false")
	}
}

func TestCheckErrorMsg_NoError(t *testing.T) {
	data := []byte(`{"success": true, "value": 42}`)
	if err := checkErrorMsg(data); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCheckErrorMsg_WithError(t *testing.T) {
	data := []byte(`{"error": "file not found"}`)
	err := checkErrorMsg(data)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Errorf("error message: got %q", err.Error())
	}
}

func TestCheckErrorMsg_EmptyError(t *testing.T) {
	data := []byte(`{"error": ""}`)
	if err := checkErrorMsg(data); err != nil {
		t.Errorf("expected no error for empty error string, got %v", err)
	}
}

func TestCheckErrorMsg_InvalidJSON(t *testing.T) {
	data := []byte(`not json`)
	if err := checkErrorMsg(data); err != nil {
		t.Errorf("expected no error for invalid JSON, got %v", err)
	}
}

func TestCheckErrorMsg_NilInput(t *testing.T) {
	if err := checkErrorMsg(nil); err != nil {
		t.Errorf("expected no error for nil, got %v", err)
	}
}

func TestHexDecode_Empty(t *testing.T) {
	b, err := hexDecode("")
	if err != nil {
		t.Fatalf("hexDecode empty: %v", err)
	}
	if len(b) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(b))
	}
	for _, v := range b {
		if v != 0 {
			t.Errorf("expected all zeros, got %d", v)
			break
		}
	}
}

func TestHexDecode_Valid(t *testing.T) {
	b, err := hexDecode("abcdef0123456789")
	if err != nil {
		t.Fatalf("hexDecode: %v", err)
	}
	if len(b) != 8 {
		t.Fatalf("expected 8 bytes (16 hex chars / 2), got %d", len(b))
	}
	if b[0] != 0xab || b[1] != 0xcd || b[2] != 0xef || b[3] != 0x01 {
		t.Errorf("first bytes: got %x %x %x %x", b[0], b[1], b[2], b[3])
	}
}

func TestHexDecode_Invalid(t *testing.T) {
	_, err := hexDecode("zzzz")
	if err == nil {
		t.Fatal("expected error for invalid hex")
	}
}

func TestExtractIP(t *testing.T) {
	tests := []struct {
		addrs []string
		want  string
	}{
		{[]string{"/ip4/192.168.1.1/tcp/4327"}, "192.168.1.1"},
		{[]string{"/ip6/::1/tcp/4327"}, "::1"},
		{[]string{"/ip4/10.0.0.1/tcp/9000", "/ip6/::1/tcp/9000"}, "10.0.0.1"},
		{[]string{}, ""},
	}
	for _, tc := range tests {
		mas := make([]multiaddr.Multiaddr, len(tc.addrs))
		for i, a := range tc.addrs {
			ma, err := multiaddr.NewMultiaddr(a)
			if err != nil {
				t.Fatalf("bad multiaddr %q: %v", a, err)
			}
			mas[i] = ma
		}
		pi := peer.AddrInfo{Addrs: mas}
		got := extractIP(pi)
		if got != tc.want {
			t.Errorf("extractIP(%v) = %q, want %q", tc.addrs, got, tc.want)
		}
	}
}

func TestFillSkipped(t *testing.T) {
	r := &PeerConnectResult{}
	fillSkipped(r, "未登录")
	if r.Phase1.Success {
		t.Error("expected Phase1.Success=false")
	}
	if r.Phase1.Error != "未登录" {
		t.Errorf("Phase1.Error: got %q", r.Phase1.Error)
	}
	if r.Phase2.Error != "跳过" {
		t.Errorf("Phase2.Error: got %q", r.Phase2.Error)
	}
	if r.Phase3Send.Error != "跳过" {
		t.Errorf("Phase3Send.Error: got %q", r.Phase3Send.Error)
	}
	if r.Phase3Recv.Error != "跳过" {
		t.Errorf("Phase3Recv.Error: got %q", r.Phase3Recv.Error)
	}
}
