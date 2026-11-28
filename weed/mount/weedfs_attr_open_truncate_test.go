//go:build !freebsd

package mount

import (
	"context"
	"fmt"
	"testing"

	"github.com/seaweedfs/go-fuse/v2/fuse"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/seaweedfs/seaweedfs/weed/filer"
	"github.com/seaweedfs/seaweedfs/weed/pb/filer_pb"
	"github.com/seaweedfs/seaweedfs/weed/util"
)

// A truncate through an open handle is on the filer when SetAttr answers, as
// one without a handle is: an NFS client sends no COMMIT after SETATTR, and a
// mount that dies before the handle's flush brought the old size back. With
// the writeback cache it waits for the flush, except under an NFS export.
func TestTruncateWithOpenHandleIsStored(t *testing.T) {
	for _, tc := range []struct {
		size                 uint64
		writeback, nfsExport bool
		stored               bool
		wantChunks           int
	}{
		{size: 300, stored: true, wantChunks: 2},
		{size: 4096, stored: true, wantChunks: 3},
		{size: 300, writeback: true},
		{size: 300, writeback: true, nfsExport: true, stored: true, wantChunks: 2},
	} {
		t.Run(fmt.Sprintf("size=%d/writeback=%t/nfsExport=%t", tc.size, tc.writeback, tc.nfsExport), func(t *testing.T) {
			wfs := newInvalidateTestWFS(t)
			wfs.option.WritebackCache, wfs.option.NfsExport = tc.writeback, tc.nfsExport
			current := attributeAppendEntry(3)
			fake := &attributeCASFiler{entry: proto.Clone(current).(*filer_pb.Entry)}
			startFakeFiler(t, wfs, fake)
			path := util.FullPath("/dir/file")
			wfs.inodeToPath.Lookup(util.FullPath("/dir"), 1, true, false, 0, false)
			wfs.inodeToPath.Lookup(path, 1, false, false, 42, true)
			wfs.inodeToPath.MarkChildrenCached(util.FullPath("/dir"))
			require.NoError(t, wfs.metaCache.InsertEntry(context.Background(), filer.FromPbEntry("/dir", current), 1))
			local := proto.Clone(current).(*filer_pb.Entry)
			wfs.mapPbIdFromFilerToLocal(local)
			fh, _, _, _ := wfs.fhMap.AcquireFileHandle(wfs, 42, local, 0, 0)
			fh.baseEntry.Store(proto.Clone(local).(*filer_pb.Entry))
			fh.RememberPath(path)
			t.Cleanup(fh.dirtyPages.Destroy)

			in := &fuse.SetAttrIn{}
			in.NodeId, in.Valid, in.Size = 42, fuse.FATTR_SIZE, tc.size
			var out fuse.AttrOut
			require.Equal(t, fuse.OK, wfs.SetAttr(nil, in, &out))
			require.Equal(t, tc.size, out.Size)

			fake.mu.Lock()
			defer fake.mu.Unlock()
			if !tc.stored {
				require.Equal(t, uint64(768), fake.entry.Attributes.FileSize)
				require.True(t, fh.dirtyMetadata, "the size is left for the handle's flush")
				return
			}
			require.Equal(t, tc.size, fake.entry.Attributes.FileSize)
			require.Len(t, fake.entry.Chunks, tc.wantChunks)
			require.Equal(t, tc.size, filer.FileSize(fake.entry))
			require.False(t, fh.dirtyMetadata)
		})
	}
}
