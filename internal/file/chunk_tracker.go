package file

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"os"
)

// ChunkTracker 基于 bitset + per-slot 写入范围追踪文件接收状态。
type ChunkTracker struct {
	totalSize   int64
	totalSlots  int
	bitmap      []uint64
	slotMaxEnd  []int64 // 每个 slot 内已写入的最大 end 位置
	totalUnique int64   // 精确的独特字节覆盖总数
	received    int     // 被 touch 过的 slot 数
}

// NewChunkTracker 创建一个新的追踪器。
func NewChunkTracker(totalSize int64) *ChunkTracker {
	totalSlots := int((totalSize + DefaultChunkSize - 1) / DefaultChunkSize)
	if totalSlots < 1 {
		totalSlots = 1
	}
	bitmapSize := (totalSlots + 63) / 64
	return &ChunkTracker{
		totalSize:  totalSize,
		totalSlots: totalSlots,
		bitmap:     make([]uint64, bitmapSize),
		slotMaxEnd: make([]int64, totalSlots),
	}
}

// LoadChunkTracker 从 bitmapPath 恢复追踪器状态（用于断点续传）。
func LoadChunkTracker(bitmapPath string, totalSize int64) (*ChunkTracker, error) {
	ct := NewChunkTracker(totalSize)

	f, err := os.Open(bitmapPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	expectedBytes := int64(len(ct.bitmap)*8 + len(ct.slotMaxEnd)*8)
	if fi.Size() != expectedBytes {
		return nil, fmt.Errorf("chunk tracker bitmap size mismatch: expected %d, got %d",
			expectedBytes, fi.Size())
	}

	if err := binary.Read(f, binary.LittleEndian, &ct.bitmap); err != nil {
		return nil, err
	}
	if err := binary.Read(f, binary.LittleEndian, &ct.slotMaxEnd); err != nil {
		return nil, err
	}

	ct.received = ct.countBits()
	ct.totalUnique = ct.computeUniqueBytes()
	if ct.totalUnique > ct.totalSize {
		ct.totalUnique = ct.totalSize
	}
	return ct, nil
}

// MarkReceived 标记 offset 处长度为 dataLen 的 chunk 为已接收。
// 处理跨 slot chunk（例如尾部 partial chunk 跨越 slot 边界）。
// 幂等：已完成的追踪器始终返回 true；完全重叠的字节不重复计数。
func (ct *ChunkTracker) MarkReceived(offset int64, dataLen int64) (bool, error) {
	if offset < 0 || dataLen <= 0 {
		if ct.totalSize == 0 {
			return true, nil
		}
		return ct.totalUnique >= ct.totalSize, fmt.Errorf("invalid offset or dataLen")
	}

	// 已完成的追踪器直接返回
	if ct.totalUnique >= ct.totalSize {
		ct.totalUnique = ct.totalSize
		return true, nil
	}

	startIdx := int(offset / DefaultChunkSize)
	endIdx := int((offset + dataLen - 1) / DefaultChunkSize)
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx >= ct.totalSlots {
		endIdx = ct.totalSlots - 1
	}
	if startIdx >= ct.totalSlots || startIdx > endIdx {
		return false, fmt.Errorf("offset %d out of range (file size: %d)", offset, ct.totalSize)
	}

	for idx := startIdx; idx <= endIdx; idx++ {
		// 更新 bitset（幂等）
		wordIdx := idx / 64
		bitIdx := idx % 64
		mask := uint64(1) << bitIdx
		if ct.bitmap[wordIdx]&mask == 0 {
			ct.bitmap[wordIdx] |= mask
			ct.received++
		}

		// 当前 chunk 在此 slot 内的区间
		slotStart := int64(idx) * DefaultChunkSize
		slotEnd := slotStart + DefaultChunkSize
		if slotEnd > ct.totalSize {
			slotEnd = ct.totalSize
		}

		portionStart := offset
		if portionStart < slotStart {
			portionStart = slotStart
		}
		portionEnd := offset + dataLen
		if portionEnd > slotEnd {
			portionEnd = slotEnd
		}
		if portionEnd <= portionStart {
			continue
		}

		if portionEnd > ct.slotMaxEnd[idx] {
			oldEffective := ct.slotMaxEnd[idx]
			if oldEffective < slotStart {
				oldEffective = slotStart
			}
			ct.totalUnique += portionEnd - oldEffective
			ct.slotMaxEnd[idx] = portionEnd
		}
	}

	if ct.totalUnique >= ct.totalSize {
		ct.totalUnique = ct.totalSize
		return true, nil
	}
	return false, nil
}

// IsComplete 返回是否所有字节均已接收。
func (ct *ChunkTracker) IsComplete() bool {
	return ct.totalUnique >= ct.totalSize
}

// MissingOffsets 返回尚未 touch 过的 slot 的起始偏移量列表。
func (ct *ChunkTracker) MissingOffsets() []int64 {
	offsets := make([]int64, 0, ct.totalSlots-ct.received)
	for idx := 0; idx < ct.totalSlots; idx++ {
		wordIdx := idx / 64
		bitIdx := idx % 64
		if ct.bitmap[wordIdx]&(1<<bitIdx) == 0 {
			offsets = append(offsets, int64(idx)*DefaultChunkSize)
		}
	}
	return offsets
}

// MissingRanges returns byte-level [start, end) ranges for slots that
// have not been fully received yet. For partially-received slots, the
// range covers only the unreceived portion based on slotMaxEnd.
func (ct *ChunkTracker) MissingRanges() [][2]int64 {
	ranges := make([][2]int64, 0, ct.totalSlots-ct.received)
	for idx := 0; idx < ct.totalSlots; idx++ {
		wordIdx := idx / 64
		bitIdx := idx % 64
		slotStart := int64(idx) * DefaultChunkSize
		slotEnd := slotStart + DefaultChunkSize
		if slotEnd > ct.totalSize {
			slotEnd = ct.totalSize
		}

		if ct.bitmap[wordIdx]&(1<<bitIdx) == 0 {
			ranges = append(ranges, [2]int64{slotStart, slotEnd})
		} else if ct.slotMaxEnd[idx] < slotEnd {
			if ct.slotMaxEnd[idx] > slotStart {
				ranges = append(ranges, [2]int64{ct.slotMaxEnd[idx], slotEnd})
			}
		}
	}
	return ranges
}

// BytesReceived 返回已覆盖的字节数（用于进度上报，单调递增）。
func (ct *ChunkTracker) BytesReceived() int64 {
	if ct.totalUnique > ct.totalSize {
		return ct.totalSize
	}
	return ct.totalUnique
}

// Save 将 bitset + slotMaxEnd 持久化到 bitmapPath。
func (ct *ChunkTracker) Save(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := binary.Write(f, binary.LittleEndian, ct.bitmap); err != nil {
		return err
	}
	return binary.Write(f, binary.LittleEndian, ct.slotMaxEnd)
}

func (ct *ChunkTracker) computeUniqueBytes() int64 {
	var total int64
	for i := 0; i < ct.totalSlots; i++ {
		end := ct.slotMaxEnd[i]
		if end > 0 {
			slotStart := int64(i) * DefaultChunkSize
			slotCap := slotStart + DefaultChunkSize
			if slotCap > ct.totalSize {
				slotCap = ct.totalSize
			}
			covered := end - slotStart
			if covered > slotCap-slotStart {
				covered = slotCap - slotStart
			}
			if covered > 0 {
				total += covered
			}
		}
	}
	if total > ct.totalSize {
		return ct.totalSize
	}
	return total
}

func (ct *ChunkTracker) countBits() int {
	count := 0
	for _, word := range ct.bitmap {
		count += bits.OnesCount64(word)
	}
	if count > ct.totalSlots {
		return ct.totalSlots
	}
	return count
}
