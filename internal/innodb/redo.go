package innodb

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Redo log layout constants (log0constants.h). Only the 8.0.30+ format is read.
const (
	LogBlockSize    = 512
	LogFileHdrSize  = 4 * LogBlockSize
	LogBlockHdrSize = 12
	LogBlockTrlSize = 4

	LOG_HEADER_FORMAT    = 0
	LOG_HEADER_START_LSN = 8
	LOG_HEADER_CREATOR   = 16
	LOG_CHECKPOINT_1     = LogBlockSize
	LOG_CHECKPOINT_2     = 3 * LogBlockSize
	LOG_CHECKPOINT_LSN   = 8

	LOG_BLOCK_HDR_NO          = 0
	LOG_BLOCK_HDR_DATA_LEN    = 4
	LOG_BLOCK_FIRST_REC_GROUP = 6
	LOG_BLOCK_EPOCH_NO        = 8

	logBlockMaxNo      = 0x40000000
	logBlockEncryptBit = 0x8000
	// Log_format::VERSION_8_0_30, the oldest format this tool reads.
	logFormatMin = 5
)

// Checkpoint is one of the two checkpoint headers of a redo file.
type Checkpoint struct {
	LSN        uint64
	ChecksumOK bool
}

// RedoFile is one #ib_redoN file, opened read-only.
type RedoFile struct {
	f                *os.File
	Path, Name       string
	Size             int64
	Format           uint32
	StartLSN, EndLSN uint64
	Creator          string
	HdrChecksumOK    bool
	Checkpoints      [2]Checkpoint
}

// Redo is the #innodb_redo directory of a datadir.
type Redo struct {
	Dir            string
	Files          []*RedoFile
	CheckpointLSN  uint64
	CheckpointFile *RedoFile
}

// OpenRedo reads the headers of every #ib_redoN file and locates the newest
// checkpoint. Files named *_tmp are unused spares and are skipped.
func OpenRedo(dir string) (*Redo, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	r := &Redo{Dir: dir}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "#ib_redo") || strings.HasSuffix(name, "_tmp") {
			continue
		}
		f, err := openRedoFile(filepath.Join(dir, name), name)
		if err != nil {
			r.Close()
			return nil, err
		}
		r.Files = append(r.Files, f)
	}
	if len(r.Files) == 0 {
		return nil, fmt.Errorf("no redo log file in %s", dir)
	}
	sort.Slice(r.Files, func(i, j int) bool { return r.Files[i].StartLSN < r.Files[j].StartLSN })
	for _, f := range r.Files {
		for _, c := range f.Checkpoints {
			if c.ChecksumOK && c.LSN > r.CheckpointLSN && f.contains(c.LSN) {
				r.CheckpointLSN, r.CheckpointFile = c.LSN, f
			}
		}
	}
	return r, nil
}

func openRedoFile(path, name string) (*RedoFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	rf := &RedoFile{f: f, Path: path, Name: name, Size: st.Size()}
	hdr := make([]byte, LogFileHdrSize)
	if _, err := f.ReadAt(hdr, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	rf.Format = binary.BigEndian.Uint32(hdr[LOG_HEADER_FORMAT:])
	rf.StartLSN = binary.BigEndian.Uint64(hdr[LOG_HEADER_START_LSN:])
	rf.EndLSN = rf.StartLSN + uint64(rf.Size) - LogFileHdrSize
	rf.Creator = cstring(hdr[LOG_HEADER_CREATOR : LOG_HEADER_CREATOR+32])
	rf.HdrChecksumOK = logBlockChecksumOK(hdr[:LogBlockSize])
	for i, off := range [2]int{LOG_CHECKPOINT_1, LOG_CHECKPOINT_2} {
		b := hdr[off : off+LogBlockSize]
		rf.Checkpoints[i] = Checkpoint{
			LSN:        binary.BigEndian.Uint64(b[LOG_CHECKPOINT_LSN:]),
			ChecksumOK: logBlockChecksumOK(b),
		}
	}
	if rf.Format < logFormatMin {
		f.Close()
		return nil, fmt.Errorf("%s: unsupported: redo log format %d (need 8.0.30 or newer)", path, rf.Format)
	}
	return rf, nil
}

func (r *Redo) Close() error {
	for _, f := range r.Files {
		if f.f != nil {
			f.f.Close()
		}
	}
	return nil
}

func (f *RedoFile) contains(lsn uint64) bool { return f.StartLSN <= lsn && lsn < f.EndLSN }

func (r *Redo) fileFor(lsn uint64) *RedoFile {
	for _, f := range r.Files {
		if f.contains(lsn) {
			return f
		}
	}
	return nil
}

func cstring(b []byte) string {
	if i := indexZero(b); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

func indexZero(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return -1
}

// logBlockChecksumOK verifies the crc32c trailer of a 512-byte log block.
func logBlockChecksumOK(b []byte) bool {
	stored := binary.BigEndian.Uint32(b[LogBlockSize-LogBlockTrlSize:])
	return stored == crc32.Checksum(b[:LogBlockSize-LogBlockTrlSize], castagnoli)
}

func lsnToHdrNo(lsn uint64) uint32   { return uint32(1 + lsn/LogBlockSize%logBlockMaxNo) }
func lsnToEpochNo(lsn uint64) uint32 { return uint32(1 + lsn/LogBlockSize/logBlockMaxNo) }

// streamBlock maps a range of LogStream.Buf back to its lsn.
type streamBlock struct {
	dataOff int
	lsn     uint64
}

// LogStream is the mtr byte stream carved out of the redo blocks, with the
// 12-byte block headers and 4-byte trailers removed. It starts at the first
// mtr that begins in the checkpoint's block. The whole valid range is held in
// memory; it is bounded by how much was written since the last checkpoint.
type LogStream struct {
	Buf      []byte
	StartLSN uint64
	EndLSN   uint64
	// Stop says why scanning ended; the tail of a killed server always ends
	// with a block whose hdr_no or checksum no longer matches.
	Stop   string
	blocks []streamBlock
}

// LSN returns the log sequence number of Buf[off].
func (s *LogStream) LSN(off int) uint64 {
	i := sort.Search(len(s.blocks), func(i int) bool { return s.blocks[i].dataOff > off }) - 1
	if i < 0 {
		return s.StartLSN
	}
	b := s.blocks[i]
	return b.lsn + uint64(off-b.dataOff)
}

// Scan collects the log blocks from the newest checkpoint up to the first
// block that no longer belongs to the current write sequence.
func (r *Redo) Scan() (*LogStream, error) {
	if r.CheckpointLSN == 0 {
		return nil, fmt.Errorf("no valid checkpoint header in %s", r.Dir)
	}
	s := &LogStream{}
	blockLSN := r.CheckpointLSN &^ (LogBlockSize - 1)
	buf := make([]byte, LogBlockSize)
	for {
		f := r.fileFor(blockLSN)
		if f == nil {
			s.Stop = fmt.Sprintf("no redo file covers lsn %d", blockLSN)
			break
		}
		fileOff := int64(LogFileHdrSize + (blockLSN - f.StartLSN))
		if _, err := f.f.ReadAt(buf, fileOff); err != nil {
			s.Stop = err.Error()
			break
		}
		dataLen := int(binary.BigEndian.Uint16(buf[LOG_BLOCK_HDR_DATA_LEN:]))
		if dataLen&logBlockEncryptBit != 0 {
			s.Stop = "unsupported: encrypted redo log"
			break
		}
		if got, want := binary.BigEndian.Uint32(buf[LOG_BLOCK_HDR_NO:]), lsnToHdrNo(blockLSN); got != want {
			s.Stop = fmt.Sprintf("end of log at lsn %d: LOG_BLOCK_HDR_NO %d, expected %d", blockLSN, got, want)
			break
		}
		if !logBlockChecksumOK(buf) {
			s.Stop = fmt.Sprintf("end of log at lsn %d: block checksum mismatch", blockLSN)
			break
		}
		if got, want := binary.BigEndian.Uint32(buf[LOG_BLOCK_EPOCH_NO:]), lsnToEpochNo(blockLSN); got != want {
			s.Stop = fmt.Sprintf("end of log at lsn %d: LOG_BLOCK_EPOCH_NO %d, expected %d", blockLSN, got, want)
			break
		}
		if dataLen < LogBlockHdrSize || dataLen > LogBlockSize {
			s.Stop = fmt.Sprintf("end of log at lsn %d: data len %d", blockLSN, dataLen)
			break
		}
		start := LogBlockHdrSize
		if len(s.blocks) == 0 {
			// Nothing is parsable until a block declares where an mtr starts.
			frg := int(binary.BigEndian.Uint16(buf[LOG_BLOCK_FIRST_REC_GROUP:]))
			if frg < LogBlockHdrSize || frg >= dataLen {
				if dataLen < LogBlockSize {
					s.Stop = fmt.Sprintf("no mtr starts at or after lsn %d", blockLSN)
					break
				}
				blockLSN += LogBlockSize
				continue
			}
			start = frg
			s.StartLSN = blockLSN + uint64(frg)
		}
		end := dataLen
		if end > LogBlockSize-LogBlockTrlSize {
			end = LogBlockSize - LogBlockTrlSize
		}
		if start < end {
			s.blocks = append(s.blocks, streamBlock{dataOff: len(s.Buf), lsn: blockLSN + uint64(start)})
			s.Buf = append(s.Buf, buf[start:end]...)
		}
		s.EndLSN = blockLSN + uint64(dataLen)
		if dataLen < LogBlockSize {
			s.Stop = fmt.Sprintf("last block is partial (data len %d) at lsn %d", dataLen, blockLSN)
			break
		}
		blockLSN += LogBlockSize
	}
	if len(s.blocks) == 0 {
		return s, fmt.Errorf("no redo data after checkpoint lsn %d: %s", r.CheckpointLSN, s.Stop)
	}
	return s, nil
}
