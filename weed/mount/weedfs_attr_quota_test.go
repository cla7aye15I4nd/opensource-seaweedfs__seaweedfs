package mount

import (
	"math"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/seaweedfs/go-fuse/v2/fuse"
)

// TestSetAttrChargesTheGrowth pins the quota accounting for a truncate up: the
// writes that fill the range do not grow the file, so they charge nothing and
// the growth has to be counted where it happens, as Write and Fallocate do.
func TestSetAttrChargesTheGrowth(t *testing.T) {
	atomic.StoreInt64(&uncommittedBytes, 0)
	wfs, fh := newOpenFileHandle(t, 10)
	wfs.option.Quota = 100 << 20
	wfs.option.WritebackCache = true // the size is stored later, by a flush this fake has no filer for

	in := &fuse.SetAttrIn{}
	in.NodeId = fh.inode
	in.Valid = fuse.FATTR_SIZE
	in.Size = 8192
	var out fuse.AttrOut
	if status := wfs.SetAttr(nil, in, &out); status != fuse.OK {
		t.Fatalf("SetAttr size up: got %v, want OK", status)
	}
	if got := wfs.GetUncommittedBytes(); got != 8192-10 {
		t.Fatalf("uncommitted bytes: got %d, want %d", got, 8192-10)
	}

	// Truncating back down frees space rather than consuming it.
	in.Size = 0
	if status := wfs.SetAttr(nil, in, &out); status != fuse.OK {
		t.Fatalf("SetAttr size down: got %v, want OK", status)
	}
	if got := wfs.GetUncommittedBytes(); got != 8192-10 {
		t.Fatalf("uncommitted bytes after a shrink: got %d, want %d", got, 8192-10)
	}
}

// A truncate up that would pass the quota is refused before the file grows,
// so neither the size nor the charge is left behind for a flush to refuse.
func TestSetAttrRefusesGrowthPastQuota(t *testing.T) {
	atomic.StoreInt64(&uncommittedBytes, 0)
	wfs, fh := newOpenFileHandle(t, 10)
	wfs.option.Quota = 100
	wfs.stats.UsedSize = 90

	in := &fuse.SetAttrIn{}
	in.NodeId = fh.inode
	in.Valid = fuse.FATTR_SIZE
	in.Size = 30
	var out fuse.AttrOut
	if status := wfs.SetAttr(nil, in, &out); status != fuse.Status(syscall.ENOSPC) {
		t.Fatalf("SetAttr size past the quota: got %v, want ENOSPC", status)
	}
	if got := fh.GetEntry().GetEntry().Attributes.FileSize; got != 10 {
		t.Fatalf("file size after the refusal: got %d, want 10", got)
	}
	if got := wfs.GetUncommittedBytes(); got != 0 {
		t.Fatalf("uncommitted bytes after the refusal: got %d, want 0", got)
	}
}

// A growth near the int64 limit is refused rather than wrapping the sum.
func TestSetAttrRefusesHugeGrowth(t *testing.T) {
	for _, size := range []uint64{math.MaxInt64, math.MaxUint64} {
		atomic.StoreInt64(&uncommittedBytes, 0)
		wfs, fh := newOpenFileHandle(t, 10)
		wfs.option.Quota = 100
		wfs.option.WritebackCache = true
		wfs.stats.UsedSize = 50

		in := &fuse.SetAttrIn{}
		in.NodeId = fh.inode
		in.Valid = fuse.FATTR_SIZE
		in.Size = size
		var out fuse.AttrOut
		if status := wfs.SetAttr(nil, in, &out); status != fuse.Status(syscall.ENOSPC) {
			t.Fatalf("SetAttr size %d: got %v, want ENOSPC", size, status)
		}
		if got := fh.GetEntry().GetEntry().Attributes.FileSize; got != 10 {
			t.Fatalf("file size after refusing %d: got %d, want 10", size, got)
		}
	}
}
