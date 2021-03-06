package block

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/erkrnt/symphony/api"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type endpoints struct {
	block *Block
}

const configTemplate = `
<target %s>
  backing-store %s
  initiator-address %s
</target>`

func reverse(ss []string) {
	last := len(ss) - 1

	for i := 0; i < len(ss)/2; i++ {
		ss[i], ss[last-i] = ss[last-i], ss[i]
	}
}

// GetLogicalVolume : gets logical volume metadata from block host
func (e *endpoints) GetLogicalVolume(ctx context.Context, in *api.BlockLogicalVolumeRequest) (*api.LogicalVolumeMetadata, error) {
	volumeGroupID, err := uuid.Parse(in.VolumeGroupID)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	id, err := uuid.Parse(in.ID)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	lv, lvErr := getLv(volumeGroupID, id)

	if lvErr != nil {
		return nil, api.ProtoError(lvErr)
	}

	if lv == nil {
		err := status.Error(codes.NotFound, "invalid_physical_volume")
		return nil, api.ProtoError(err)
	}

	metadata := &api.LogicalVolumeMetadata{
		LvName:          lv.LvName,
		VgName:          lv.VgName,
		LvAttr:          lv.LvAttr,
		LvSize:          lv.LvSize,
		PoolLv:          lv.PoolLv,
		Origin:          lv.Origin,
		DataPercent:     lv.DataPercent,
		MetadataPercent: lv.MetadataPercent,
		MovePv:          lv.MovePv,
		MirrorLog:       lv.MirrorLog,
		CopyPercent:     lv.CopyPercent,
		ConvertLv:       lv.ConvertLv,
	}

	logFields := logrus.Fields{
		"ID":            id.String(),
		"VolumeGroupID": volumeGroupID.String(),
	}

	logrus.WithFields(logFields).Info("GetLv")

	return metadata, nil
}

// GetPhysicalVolume : gets physical volume
func (e *endpoints) GetPhysicalVolume(ctx context.Context, in *api.BlockPhysicalVolumeRequest) (*api.PhysicalVolumeMetadata, error) {
	metadata, pvErr := getPv(in.DeviceName)

	if pvErr != nil {
		return nil, api.ProtoError(pvErr)
	}

	if metadata == nil {
		err := status.Error(codes.NotFound, "invalid_physical_volume")

		return nil, api.ProtoError(err)
	}

	logrus.WithFields(logrus.Fields{"DeviceName": in.DeviceName}).Info("GetPv")

	return metadata, nil
}

// GetVolumeGroup : gets volume group
func (e *endpoints) GetVolumeGroup(ctx context.Context, in *api.BlockVolumeGroupRequest) (*api.VolumeGroupMetadata, error) {
	id, err := uuid.Parse(in.ID)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	vg, err := getVg(id)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	metadata := &api.VolumeGroupMetadata{
		VgName:    vg.VgName,
		PvCount:   vg.PvCount,
		LvCount:   vg.LvCount,
		SnapCount: vg.SnapCount,
		VgAttr:    vg.VgAttr,
		VgSize:    vg.VgSize,
		VgFree:    vg.VgFree,
	}

	logrus.WithFields(logrus.Fields{"ID": id.String()}).Info("GetVg")

	return metadata, nil
}

// NewLogicalVolume : creates logical volume
func (e *endpoints) NewLogicalVolume(ctx context.Context, in *api.BlockNewLogicalVolumeRequest) (*api.BlockNewLogicalVolumeResponse, error) {
	volumeGroupID, err := uuid.Parse(in.VolumeGroupID)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	id, err := uuid.Parse(in.ID)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	lv, err := newLv(volumeGroupID, id, in.Size)

	if err != nil {
		return nil, err
	}

	// TODO : get the image and burn into volume

	currentTime := time.Now()

	backingStore := fmt.Sprintf("/dev/%s/%s", volumeGroupID.String(), id.String())

	targetAddrDate := fmt.Sprintf("%d-%02d", currentTime.Year(), currentTime.Month())

	targetIPAddrString := e.block.flags.listenAddr.IP.String()

	targetIPAddrArray := strings.Split(targetIPAddrString, ".")

	reverse(targetIPAddrArray)

	targetIPAddrFQN := strings.Join(targetIPAddrArray, ".")

	targetAddr := fmt.Sprintf("iqn.%s.%s.in-addr.arpa:%s", targetAddrDate, targetIPAddrFQN, id.String())

	config := fmt.Sprintf(configTemplate, targetAddr, backingStore, e.block.flags.listenAddr.IP.String())

	path := fmt.Sprintf("/etc/tgt/conf.d/%s.conf", id.String())

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

	if err != nil {
		return nil, err
	}

	defer file.Close()

	data := []byte(config)

	_, writeErr := file.Write(data)

	if writeErr != nil {
		return nil, writeErr
	}

	_, tgtErr := exec.Command("tgt-admin", "--update", "ALL").Output()

	if tgtErr != nil {
		return nil, tgtErr
	}

	metadata := &api.LogicalVolumeMetadata{
		LvName:          lv.LvName,
		VgName:          lv.VgName,
		LvAttr:          lv.LvAttr,
		LvSize:          lv.LvSize,
		PoolLv:          lv.PoolLv,
		Origin:          lv.Origin,
		DataPercent:     lv.DataPercent,
		MetadataPercent: lv.MetadataPercent,
		MovePv:          lv.MovePv,
		MirrorLog:       lv.MirrorLog,
		CopyPercent:     lv.CopyPercent,
		ConvertLv:       lv.ConvertLv,
	}

	logFields := logrus.Fields{
		"ID":            id.String(),
		"Size":          in.Size,
		"VolumeGroupID": volumeGroupID.String(),
	}

	logrus.WithFields(logFields).Info("NewLv")

	res := &api.BlockNewLogicalVolumeResponse{
		Metadata:   metadata,
		TargetAddr: targetAddr,
	}

	return res, nil
}

// NewPhysicalVolume : creates physical volume
func (e *endpoints) NewPhysicalVolume(ctx context.Context, in *api.BlockPhysicalVolumeRequest) (*api.PhysicalVolumeMetadata, error) {
	pv, err := newPv(in.DeviceName)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	metadata := &api.PhysicalVolumeMetadata{
		PvName: pv.PvName,
		VgName: pv.VgName,
		PvFmt:  pv.PvFmt,
		PvAttr: pv.PvAttr,
		PvSize: pv.PvSize,
		PvFree: pv.PvFree,
	}

	logrus.WithFields(logrus.Fields{"DeviceName": in.DeviceName}).Info("NewPv")

	return metadata, nil
}

// NewVolumeGroup : creates volume group
func (e *endpoints) NewVolumeGroup(ctx context.Context, in *api.BlockNewVolumeGroupRequest) (*api.VolumeGroupMetadata, error) {
	id, err := uuid.Parse(in.ID)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	vg, err := newVg(in.DeviceName, id)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	metadata := &api.VolumeGroupMetadata{
		VgName:    vg.VgName,
		PvCount:   vg.PvCount,
		LvCount:   vg.LvCount,
		SnapCount: vg.SnapCount,
		VgAttr:    vg.VgAttr,
		VgSize:    vg.VgSize,
		VgFree:    vg.VgFree,
	}

	logrus.WithFields(logrus.Fields{"ID": id.String()}).Info("NewVg")

	return metadata, nil
}

// RemoveLogicalVolume : removes logical volume
func (e *endpoints) RemoveLogicalVolume(ctx context.Context, in *api.BlockLogicalVolumeRequest) (*api.SuccessStatusResponse, error) {
	volumeGroupID, err := uuid.Parse(in.VolumeGroupID)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	id, err := uuid.Parse(in.ID)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	path := fmt.Sprintf("/etc/tgt/conf.d/%s.conf", id.String())

	removeTgtConfigErr := os.Remove(path)

	if removeTgtConfigErr != nil {
		return nil, removeTgtConfigErr
	}

	_, tgtErr := exec.Command("tgt-admin", "--update", "ALL").Output()

	if tgtErr != nil {
		return nil, tgtErr
	}

	rmErr := removeLv(volumeGroupID, id)

	if rmErr != nil {
		return nil, api.ProtoError(rmErr)
	}

	status := &api.SuccessStatusResponse{Success: true}

	logrus.WithFields(logrus.Fields{"Success": status.Success}).Info("RemoveLv")

	return status, nil
}

// RemovePhysicalVolume : removes physical volume
func (e *endpoints) RemovePhysicalVolume(ctx context.Context, in *api.BlockPhysicalVolumeRequest) (*api.SuccessStatusResponse, error) {
	err := removePv(in.DeviceName)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	status := &api.SuccessStatusResponse{Success: true}

	logrus.WithFields(logrus.Fields{"Success": status.Success}).Info("RemovePv")

	return status, nil
}

// RemoveVolumeGroup : removes volume group
func (e *endpoints) RemoveVolumeGroup(ctx context.Context, in *api.BlockVolumeGroupRequest) (*api.SuccessStatusResponse, error) {
	id, err := uuid.Parse(in.ID)

	if err != nil {
		return nil, api.ProtoError(err)
	}

	rmErr := removeVg(id)

	if rmErr != nil {
		return nil, api.ProtoError(rmErr)
	}

	status := &api.SuccessStatusResponse{Success: true}

	logrus.WithFields(logrus.Fields{"Success": status.Success}).Info("RemoveVg")

	return status, nil
}

func (e *endpoints) ServiceInit(ctx context.Context, in *api.BlockServiceInitRequest) (*api.BlockServiceInitResponse, error) {
	initAddr, err := net.ResolveTCPAddr("tcp", in.ServiceAddr)

	if err != nil {
		return nil, err
	}

	conn, err := grpc.Dial(initAddr.String(), grpc.WithInsecure())

	if err != nil {
		return nil, err
	}

	defer conn.Close()

	r := api.NewManagerClient(conn)

	serviceAddr := fmt.Sprintf("%s", e.block.flags.listenAddr.String())

	opts := &api.ManagerServiceInitRequest{
		ServiceAddr: serviceAddr,
		ServiceType: api.ServiceType_BLOCK,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()

	init, err := r.ServiceInit(ctx, opts)

	if err != nil {
		return nil, err
	}

	clusterID, err := uuid.Parse(init.ClusterID)

	if err != nil {
		return nil, err
	}

	serviceID, err := uuid.Parse(init.ServiceID)

	if err != nil {
		return nil, err
	}

	e.block.Node.Key.ClusterID = &clusterID

	e.block.Node.Key.Endpoints = init.Endpoints

	e.block.Node.Key.ServiceID = &serviceID

	saveErr := e.block.Node.Key.Save(e.block.flags.configDir)

	if saveErr != nil {
		st := status.New(codes.Internal, saveErr.Error())

		return nil, st.Err()
	}

	restartErr := e.block.restart()

	if restartErr != nil {
		st := status.New(codes.Internal, restartErr.Error())

		return nil, st.Err()
	}

	res := &api.BlockServiceInitResponse{
		ClusterID: clusterID.String(),
		ServiceID: serviceID.String(),
	}

	return res, nil
}

func (e *endpoints) ServiceLeave(ctx context.Context, in *api.BlockServiceLeaveRequest) (*api.SuccessStatusResponse, error) {
	serviceID, err := uuid.Parse(in.ServiceID)

	if err != nil {
		st := status.New(codes.InvalidArgument, err.Error())

		return nil, st.Err()
	}

	if serviceID != *e.block.Node.Key.ServiceID {
		st := status.New(codes.PermissionDenied, err.Error())

		return nil, st.Err()
	}

	stopErr := e.block.Node.StopSerf()

	if stopErr != nil {
		st := status.New(codes.Internal, err.Error())

		return nil, st.Err()
	}

	logrus.Debug("Block service has left the cluster.")

	res := &api.SuccessStatusResponse{}

	return res, nil
}
