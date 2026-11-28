package weed_server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/seaweedfs/seaweedfs/weed/pb/volume_server_pb"
	"github.com/seaweedfs/seaweedfs/weed/storage/needle"
)

type volumeCopyContextStream struct {
	volume_server_pb.VolumeServer_VolumeCopyServer
}

func (volumeCopyContextStream) Context() context.Context { return context.Background() }

func TestVolumeHandlersRejectPathCollection(t *testing.T) {
	ctx := context.Background()
	for _, collection := range []string{"..", "../outside", "a/b", `a\b`} {
		vs := &VolumeServer{}
		calls := map[string]func() error{
			"AllocateVolume": func() error {
				_, err := vs.AllocateVolume(ctx, &volume_server_pb.AllocateVolumeRequest{VolumeId: 7, Collection: collection})
				return err
			},
			"VolumeMount": func() error {
				_, err := vs.VolumeMount(ctx, &volume_server_pb.VolumeMountRequest{VolumeId: 7, Collection: &collection})
				return err
			},
			"VolumeCopy": func() error {
				return vs.VolumeCopy(&volume_server_pb.VolumeCopyRequest{VolumeId: 7, Collection: collection}, volumeCopyContextStream{})
			},
			"VolumeEcShardsRebuild": func() error {
				_, err := vs.VolumeEcShardsRebuild(ctx, &volume_server_pb.VolumeEcShardsRebuildRequest{VolumeId: 7, Collection: collection})
				return err
			},
			"VolumeEcShardsCopy": func() error {
				_, err := vs.VolumeEcShardsCopy(ctx, &volume_server_pb.VolumeEcShardsCopyRequest{VolumeId: 7, Collection: collection})
				return err
			},
			"VolumeEcShardsDelete": func() error {
				_, err := vs.VolumeEcShardsDelete(ctx, &volume_server_pb.VolumeEcShardsDeleteRequest{VolumeId: 7, Collection: collection, FullTeardown: true})
				return err
			},
			"VolumeEcShardsMount": func() error {
				_, err := vs.VolumeEcShardsMount(ctx, &volume_server_pb.VolumeEcShardsMountRequest{VolumeId: 7, Collection: collection, ShardIds: []uint32{0}})
				return err
			},
			"VolumeEcShardsToVolume": func() error {
				_, err := vs.VolumeEcShardsToVolume(ctx, &volume_server_pb.VolumeEcShardsToVolumeRequest{VolumeId: 7, Collection: collection, FromStaged: true})
				return err
			},
		}
		for name, call := range calls {
			err := call()
			if err == nil || !strings.Contains(err.Error(), "invalid collection") {
				t.Errorf("%s(collection=%q) = %v, want an invalid collection error", name, collection, err)
			}
		}
	}
}

type pathCollectionSource struct {
	volume_server_pb.UnimplementedVolumeServerServer
}

func (pathCollectionSource) ReadVolumeFileStatus(ctx context.Context, req *volume_server_pb.ReadVolumeFileStatusRequest) (*volume_server_pb.ReadVolumeFileStatusResponse, error) {
	return &volume_server_pb.ReadVolumeFileStatusResponse{VolumeId: req.VolumeId, Collection: "../outside"}, nil
}

func TestVolumeCopyRejectsSourcePathCollection(t *testing.T) {
	const vid = needle.VolumeId(43)
	port := serveGrpc(t, func(server *grpc.Server) {
		volume_server_pb.RegisterVolumeServerServer(server, pathCollectionSource{})
	})

	targetDir := filepath.Join(t.TempDir(), "volumes")
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := &VolumeServer{
		store:                         newVolumeCopyTestStore(t, targetDir),
		grpcDialOption:                grpc.WithTransportCredentials(insecure.NewCredentials()),
		AllowUntrustedRemoteEndpoints: true,
	}
	err := target.VolumeCopy(&volume_server_pb.VolumeCopyRequest{
		VolumeId:       uint32(vid),
		SourceDataNode: fmt.Sprintf("127.0.0.1:%d.%d", port-10000, port),
	}, &fakeVolumeCopyStream{})
	if err == nil || !strings.Contains(err.Error(), "invalid collection") {
		t.Fatalf("VolumeCopy error = %v, want an invalid collection error", err)
	}
	if leaked, _ := filepath.Glob(filepath.Join(filepath.Dir(targetDir), "outside_43*")); len(leaked) > 0 {
		t.Fatalf("VolumeCopy wrote outside the volume directory: %v", leaked)
	}
}
