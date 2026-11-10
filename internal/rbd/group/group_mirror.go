/*
Copyright 2025 The Ceph-CSI Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package group

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ceph/go-ceph/rados"
	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/ceph/go-ceph/rbd/admin"

	rbderrors "github.com/ceph/ceph-csi/internal/rbd/errors"
	"github.com/ceph/ceph-csi/internal/rbd/types"
	"github.com/ceph/ceph-csi/internal/util/log"
)

type volumeGroupMirror volumeGroup

func (vgm *volumeGroupMirror) EnableMirroring(ctx context.Context, mode librbd.ImageMirrorMode) error {
	name, err := vgm.GetName(ctx)
	if err != nil {
		return err
	}

	ioctx, err := vgm.GetIOContext(ctx)
	if err != nil {
		return err
	}

	err = librbd.MirrorGroupEnable(ioctx, name, mode)
	if err != nil {
		return fmt.Errorf("failed to enable mirroring on volume group %q: %w", vgm, err)
	}

	log.DebugLog(ctx, "mirroring is enabled on the volume group %q", vgm)

	return nil
}

func (vgm *volumeGroupMirror) DisableMirroring(ctx context.Context, force bool) error {
	name, err := vgm.GetName(ctx)
	if err != nil {
		return err
	}

	ioctx, err := vgm.GetIOContext(ctx)
	if err != nil {
		return err
	}

	err = librbd.MirrorGroupDisable(ioctx, name, force)
	if err != nil && !errors.Is(err, rados.ErrNotFound) {
		return fmt.Errorf("failed to disable mirroring on volume group %q: %w", vgm, err)
	}

	log.DebugLog(ctx, "mirroring is disabled on the volume group %q", vgm)

	return nil
}

func (vgm *volumeGroupMirror) Promote(ctx context.Context, force bool) error {
	name, err := vgm.GetName(ctx)
	if err != nil {
		return err
	}

	ioctx, err := vgm.GetIOContext(ctx)
	if err != nil {
		return err
	}

	err = librbd.MirrorGroupPromote(ioctx, name, force)
	if err != nil {
		return fmt.Errorf("failed to promote volume group %q: %w", vgm, err)
	}

	log.DebugLog(ctx, "volume group %q has been promoted with force=%v", vgm, force)

	return nil
}

func (vgm *volumeGroupMirror) Demote(ctx context.Context) error {
	name, err := vgm.GetName(ctx)
	if err != nil {
		return err
	}

	ioctx, err := vgm.GetIOContext(ctx)
	if err != nil {
		return err
	}

	err = librbd.MirrorGroupDemote(ioctx, name)
	if err != nil {
		return fmt.Errorf("failed to demote volume group %q: %w", vgm, err)
	}

	log.DebugLog(ctx, "volume group %q has been demoted", vgm)

	return nil
}

func (vgm *volumeGroupMirror) Resync(ctx context.Context) error {
	name, err := vgm.GetName(ctx)
	if err != nil {
		return err
	}

	ioctx, err := vgm.GetIOContext(ctx)
	if err != nil {
		return err
	}

	err = librbd.MirrorGroupResync(ioctx, name)
	if err != nil {
		return fmt.Errorf("failed to resync volume group %q: %w", vgm, err)
	}

	log.DebugLog(ctx, "issued resync on volume group %q", vgm)

	// delay until the state is syncing, or until 1+2+4+8+16 seconds passed
	delay := 1 * time.Second
	for {
		time.Sleep(delay)

		sts, dErr := vgm.GetGlobalMirroringStatus(ctx)
		if dErr != nil {
			// the image gets recreated after issuing resync
			if errors.Is(dErr, rbderrors.ErrImageNotFound) {
				continue
			}
			log.ErrorLog(ctx, dErr.Error())

			return dErr
		}

		localStatus, dErr := sts.GetLocalSiteStatus()
		if dErr != nil {
			log.ErrorLog(ctx, dErr.Error())

			return fmt.Errorf("failed to get local status: %w", dErr)
		}

		syncInfo, dErr := localStatus.GetLastSyncInfo(ctx)
		if dErr != nil {
			return fmt.Errorf("failed to get last sync info: %w", dErr)
		}
		if syncInfo.IsSyncing() {
			return nil
		}

		delay = 2 * delay
		if delay > 30 {
			break
		}
	}

	// If we issued a resync, return a non-final error as image needs to be recreated
	// locally. Caller retries till RBD syncs an initial version of the image to
	// report its status in the resync request.
	return fmt.Errorf("%w: awaiting initial resync due to split brain", rbderrors.ErrGroupUnavailable)
}

func (vgm *volumeGroupMirror) GetMirroringInfo(ctx context.Context) (types.MirrorInfo, error) {
	name, err := vgm.GetName(ctx)
	if err != nil {
		return nil, err
	}

	ioctx, err := vgm.GetIOContext(ctx)
	if err != nil {
		return nil, err
	}

	info, err := librbd.GetMirrorGroupInfo(ioctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get volume group mirroring info %q: %w", vgm, err)
	}

	gi := groupInfo(*info)

	return gi, nil
}

func (vgm *volumeGroupMirror) GetGlobalMirroringStatus(ctx context.Context) (types.GlobalStatus, error) {
	name, err := vgm.GetName(ctx)
	if err != nil {
		return nil, err
	}

	ioctx, err := vgm.GetIOContext(ctx)
	if err != nil {
		return nil, err
	}
	statusInfo, err := librbd.GetGlobalMirrorGroupStatus(ioctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get volume group mirroring status %q: %w", vgm, err)
	}

	gms := globalMirrorGroupStatus(statusInfo)

	return &gms, nil
}

func (vgm *volumeGroupMirror) AddSnapshotScheduling(interval admin.Interval, startTime admin.StartTime) error {
	ls := admin.NewGroupLevelSpec(vgm.pool, vgm.namespace, vgm.name)
	ra, err := vgm.conn.GetRBDAdmin()
	if err != nil {
		return err
	}
	adminConn := ra.GroupSnapshotSchedule()
	err = adminConn.Add(ls, interval, startTime)
	if err != nil {
		return err
	}

	return nil
}

// groupInfo is a wrapper around librbd.MirrorGroupInfo that contains the
// group mirror info.
type groupInfo librbd.MirrorGroupInfo

func (info groupInfo) GetState() string {
	return info.State.String()
}

func (info groupInfo) IsPrimary() bool {
	return info.Primary
}

// globalMirrorGroupStatus is a wrapper around librbd.GlobalGroupMirrorImageStatus that contains the
// global mirror group status.
type globalMirrorGroupStatus librbd.GlobalMirrorGroupStatus

func (status *globalMirrorGroupStatus) GetState() string {
	return status.Info.State.String()
}

func (status *globalMirrorGroupStatus) IsPrimary() bool {
	return status.Info.Primary
}

func (status *globalMirrorGroupStatus) GetLocalSiteStatus() (types.SiteStatus, error) {
	mstatus := librbd.GlobalMirrorGroupStatus(*status)
	s, err := mstatus.LocalStatus()
	if err != nil {
		err = fmt.Errorf("failed to get local site status: %w", err)
	}

	smgs := siteMirrorGroupStatus(s)

	return &smgs, err
}

func (status *globalMirrorGroupStatus) GetAllSitesStatus() []types.SiteStatus {
	var siteStatuses []types.SiteStatus
	for _, ss := range status.SiteStatuses {
		smgs := siteMirrorGroupStatus(ss)
		siteStatuses = append(siteStatuses, &smgs)
	}

	return siteStatuses
}

// RemoteStatus returns one SiteMirrorGroupStatus item from the SiteStatuses
// slice that corresponds to the remote site's status. If the remote status
// is not found than the error ErrStatusNotFound will be returned.
func (status *globalMirrorGroupStatus) GetRemoteSiteStatus(ctx context.Context) (types.SiteStatus, error) {
	var (
		ss  librbd.SiteMirrorGroupStatus
		err error = rbderrors.ErrStatusNotFound
	)

	for i := range status.SiteStatuses {
		log.DebugLog(
			ctx,
			"Site status of MirrorUUID: %s, state: %s, description: %s, lastUpdate: %v, up: %t",
			status.SiteStatuses[i].MirrorUUID,
			status.SiteStatuses[i].State,
			status.SiteStatuses[i].Description,
			status.SiteStatuses[i].LastUpdate,
			status.SiteStatuses[i].Up)

		if status.SiteStatuses[i].MirrorUUID != "" {
			ss = status.SiteStatuses[i]
			err = nil

			break
		}
	}

	grmss := siteMirrorGroupStatus(ss)

	return &grmss, err
}

// siteMirrorGroupStatus is a wrapper around librbd.SiteMirrorGroupStatus that contains the
// site mirror group status.
type siteMirrorGroupStatus librbd.SiteMirrorGroupStatus

func (status *siteMirrorGroupStatus) GetMirrorUUID() string {
	return status.MirrorUUID
}

func (status *siteMirrorGroupStatus) GetState() string {
	return status.State.String()
}

func (status *siteMirrorGroupStatus) GetDescription() string {
	return status.Description
}

func (status *siteMirrorGroupStatus) IsUP() bool {
	return status.Up
}

func (status *siteMirrorGroupStatus) GetLastUpdate() time.Time {
	// convert the last update time to UTC
	return time.Unix(status.LastUpdate, 0).UTC()
}

func (status *siteMirrorGroupStatus) GetLastSyncInfo(ctx context.Context) (types.SyncInfo, error) {
	return newSyncInfo(ctx, status.Description)
}

type syncInfo struct {
	LocalSnapshotTime    int64       `json:"local_snapshot_timestamp"`
	LastSnapshotBytes    int64       `json:"last_snapshot_bytes"`
	LastSnapshotDuration *int64      `json:"last_snapshot_complete_seconds"`
	ReplayState          replayState `json:"replay_state"`
}

type replayState string

const (
	idle    replayState = "idle"
	syncing replayState = "syncing"
)

// Type assertion for ensuring an implementation of the full SyncInfo interface.
var _ types.SyncInfo = &syncInfo{}

func newSyncInfo(ctx context.Context, description string) (types.SyncInfo, error) {
	// Format of the description will be as followed:
	// description = `replaying, {"bytes_per_second":0.0,"bytes_per_snapshot":81920.0,
	// "last_snapshot_bytes":81920,"last_snapshot_sync_seconds":0,
	// "local_snapshot_timestamp":1684675261,
	// "remote_snapshot_timestamp":1684675261,"replay_state":"idle"}`
	// In case there is no last snapshot bytes returns 0 as the
	// LastSyncBytes is optional.
	// In case there is no last snapshot sync seconds, it returns nil as the
	// LastSyncDuration is optional.
	// In case there is no local snapshot timestamp return an error as the
	// LastSyncTime is required.

	if description == "" {
		return nil, fmt.Errorf("empty description: %w", rbderrors.ErrLastSyncTimeNotFound)
	}
	log.DebugLog(ctx, "description: %s", description)
	splittedString := strings.SplitN(description, ",", 2)
	if len(splittedString) == 1 {
		return nil, fmt.Errorf("no snapshot details: %w", rbderrors.ErrLastSyncTimeNotFound)
	}

	var localSnapInfo syncInfo
	err := json.Unmarshal([]byte(splittedString[1]), &localSnapInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal description %q into syncInfo: %w", description, err)
	}

	// If the json unmarsal is successful but the local snapshot time is 0, we
	// need to consider it as an error as the LastSyncTime is required.
	if localSnapInfo.LocalSnapshotTime == 0 {
		return nil, fmt.Errorf("empty local snapshot timestamp: %w", rbderrors.ErrLastSyncTimeNotFound)
	}

	return &localSnapInfo, nil
}

func (si *syncInfo) GetLastSyncTime() time.Time {
	// converts localSnapshotTime of type int64 to time.Time
	return time.Unix(si.LocalSnapshotTime, 0)
}

func (si *syncInfo) GetLastSyncBytes() int64 {
	return si.LastSnapshotBytes
}

func (si *syncInfo) GetLastSyncDuration() *time.Duration {
	var duration time.Duration

	if si.LastSnapshotDuration == nil {
		duration = time.Duration(0)
	} else {
		// time.Duration is in nanoseconds
		duration = time.Duration(*si.LastSnapshotDuration) * time.Second
	}

	return &duration
}

func (si *syncInfo) IsSyncing() bool {
	return si.ReplayState == syncing
}
