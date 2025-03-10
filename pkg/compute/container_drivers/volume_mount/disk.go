// Copyright 2019 Yunion
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package volume_mount

import (
	"context"

	"yunion.io/x/pkg/errors"

	"yunion.io/x/onecloud/pkg/apis"
	api "yunion.io/x/onecloud/pkg/apis/compute"
	"yunion.io/x/onecloud/pkg/compute/models"
	"yunion.io/x/onecloud/pkg/httperrors"
	"yunion.io/x/onecloud/pkg/mcclient"
)

func init() {
	models.RegisterContainerVolumeMountDriver(newDisk())
}

type iDiskOverlay interface {
	validatePodCreateData(ctx context.Context, userCred mcclient.TokenCredential, input *apis.ContainerVolumeMountDiskOverlay, disk *api.DiskConfig) error
	validateCreateData(ctx context.Context, userCred mcclient.TokenCredential, input *apis.ContainerVolumeMountDiskOverlay, obj *models.SDisk) error
}

type disk struct {
	overlayDrivers map[apis.ContainerDiskOverlayType]iDiskOverlay
}

func newDisk() models.IContainerVolumeMountDriver {
	return &disk{
		overlayDrivers: map[apis.ContainerDiskOverlayType]iDiskOverlay{
			apis.CONTAINER_DISK_OVERLAY_TYPE_DIRECTORY:  newDiskOverlayDir(),
			apis.CONTAINER_DISK_OVERLAY_TYPE_DISK_IMAGE: newDiskOverlayImage(),
		},
	}
}

func (d disk) GetType() apis.ContainerVolumeMountType {
	return apis.CONTAINER_VOLUME_MOUNT_TYPE_DISK
}

func (d disk) validateCreateData(ctx context.Context, userCred mcclient.TokenCredential, vm *apis.ContainerVolumeMount) (*apis.ContainerVolumeMount, error) {
	disk := vm.Disk
	if disk == nil {
		return nil, httperrors.NewNotEmptyError("disk is nil")
	}
	if disk.Index == nil && disk.Id == "" {
		return nil, httperrors.NewNotEmptyError("one of index or id is required")
	}
	if disk.Index != nil {
		if *disk.Index < 0 {
			return nil, httperrors.NewInputParameterError("index is less than 0")
		}
	}
	return vm, nil
}

func (d disk) validateCaseInsensitive(disk *models.SDisk, vm *apis.ContainerVolumeMountDisk) error {
	if len(vm.CaseInsensitivePaths) == 0 {
		return nil
	}
	if disk.FsFeatures == nil {
		return httperrors.NewInputParameterError("disk(%s) fs_features is not set", disk.GetId())
	}
	if disk.FsFeatures.Ext4 == nil {
		return httperrors.NewInputParameterError("disk(%s) fs_features.ext4 is not set", disk.GetId())
	}
	if !disk.FsFeatures.Ext4.CaseInsensitive {
		return httperrors.NewInputParameterError("disk(%s) fs_features.ext4.case_insensitive is not set", disk.GetId())
	}
	if vm.Overlay != nil {
		return httperrors.NewInputParameterError("can't use case_insensitive and overlay at the same time")
	}
	if vm.SubDirectory == "" {
		return httperrors.NewInputParameterError("sub_directory must set to use case_insensitive")
	}
	return nil
}

func (d disk) ValidateCreateData(ctx context.Context, userCred mcclient.TokenCredential, pod *models.SGuest, vm *apis.ContainerVolumeMount) (*apis.ContainerVolumeMount, error) {
	if _, err := d.validateCreateData(ctx, userCred, vm); err != nil {
		return nil, err
	}
	disks, err := pod.GetDisks()
	if err != nil {
		return nil, errors.Wrap(err, "get pod disks")
	}
	disk := vm.Disk
	var diskObj models.SDisk
	if disk.Index != nil {
		diskIndex := *disk.Index
		if diskIndex >= len(disks) {
			return nil, httperrors.NewInputParameterError("disk.index %d is large than disk size %d", diskIndex, len(disks))
		}
		diskObj = disks[diskIndex]
		vm.Disk.Id = diskObj.GetId()
		// remove index
		vm.Disk.Index = nil
		if err := d.validateCaseInsensitive(&diskObj, disk); err != nil {
			return nil, err
		}
	} else {
		if disk.Id == "" {
			return nil, httperrors.NewNotEmptyError("disk.id is empty")
		}
		foundDisk := false
		for _, d := range disks {
			if d.GetId() == disk.Id || d.GetName() == disk.Id {
				disk.Id = d.GetId()
				diskObj = d
				foundDisk = true
				break
			}
		}
		if !foundDisk {
			return nil, httperrors.NewNotFoundError("not found pod disk by %s", disk.Id)
		}
		if err := d.validateCaseInsensitive(&diskObj, disk); err != nil {
			return nil, err
		}
	}
	if err := d.validateOverlay(ctx, userCred, vm, &diskObj); err != nil {
		return nil, errors.Wrapf(err, "validate overlay")
	}
	if err := d.ValidatePostOverlay(vm); err != nil {
		return nil, errors.Wrap(err, "validate post overlay")
	}
	return vm, nil
}

func (d disk) ValidatePodCreateData(ctx context.Context, userCred mcclient.TokenCredential, vm *apis.ContainerVolumeMount, input *api.ServerCreateInput) error {
	if _, err := d.validateCreateData(ctx, userCred, vm); err != nil {
		return err
	}
	disk := vm.Disk
	if disk.Id != "" {
		return httperrors.NewInputParameterError("can't specify disk_id %s when creating pod", disk.Id)
	}
	if disk.Index == nil {
		return httperrors.NewNotEmptyError("disk.index is required")
	}
	diskIndex := *disk.Index
	disks := input.Disks
	if diskIndex < 0 {
		return httperrors.NewInputParameterError("disk.index %d is less than 0", diskIndex)
	}
	if diskIndex >= len(disks) {
		return httperrors.NewInputParameterError("disk.index %d is large than disk size %d", diskIndex, len(disks))
	}
	inputDisk := disks[diskIndex]
	if vm.Disk.Overlay != nil {
		if err := d.getOverlayDriver(vm.Disk.Overlay).validatePodCreateData(ctx, userCred, vm.Disk.Overlay, inputDisk); err != nil {
			return httperrors.NewInputParameterError("valid overlay %v", err)
		}
	}
	return nil
}

func (d disk) getOverlayDriver(ov *apis.ContainerVolumeMountDiskOverlay) iDiskOverlay {
	return d.overlayDrivers[ov.GetType()]
}

func (d disk) validateOverlay(ctx context.Context, userCred mcclient.TokenCredential, vm *apis.ContainerVolumeMount, diskObj *models.SDisk) error {
	if vm.Disk.Overlay == nil {
		return nil
	}
	ov := vm.Disk.Overlay
	if err := ov.IsValid(); err != nil {
		return httperrors.NewInputParameterError("invalid overlay input: %v", err)
	}
	if err := d.getOverlayDriver(ov).validateCreateData(ctx, userCred, ov, diskObj); err != nil {
		return errors.Wrapf(err, "validate overlay %s", ov.GetType())
	}
	return nil
}

func (d disk) ValidatePostOverlay(vm *apis.ContainerVolumeMount) error {
	if len(vm.Disk.PostOverlay) == 0 {
		return nil
	}
	ovs := vm.Disk.PostOverlay
	var duplicateCtrDir string
	for _, ov := range ovs {
		if len(ov.HostLowerDir) == 0 {
			return httperrors.NewNotEmptyError("host_lower_dir is required")
		}
		for i, hld := range ov.HostLowerDir {
			if len(hld) == 0 {
				return httperrors.NewNotEmptyError("host_lower_dir %d is empty", i)
			}
		}
		if len(ov.ContainerTargetDir) == 0 {
			return httperrors.NewNotEmptyError("container_target_dir is required")
		}
		if ov.ContainerTargetDir == duplicateCtrDir {
			return httperrors.NewDuplicateNameError("container_target_dir", ov.ContainerTargetDir)
		}
		duplicateCtrDir = ov.ContainerTargetDir
	}
	if vm.Propagation == "" {
		// 设置默认 propagation 为 rslave
		vm.Propagation = apis.MOUNTPROPAGATION_PROPAGATION_HOST_TO_CONTAINER
	}
	return nil
}

type diskOverlayDir struct{}

func newDiskOverlayDir() iDiskOverlay {
	return &diskOverlayDir{}
}

func (d diskOverlayDir) validateCommonCreateData(ctx context.Context, userCred mcclient.TokenCredential, input *apis.ContainerVolumeMountDiskOverlay) error {
	if len(input.LowerDir) == 0 {
		return httperrors.NewNotEmptyError("lower_dir is required")
	}
	for idx, ld := range input.LowerDir {
		if ld == "" {
			return httperrors.NewNotEmptyError("empty %d dir", idx)
		}
		if ld == "/" {
			return httperrors.NewInputParameterError("can't use '/' as lower_dir")
		}
	}
	return nil
}

func (d diskOverlayDir) validateCreateData(ctx context.Context, userCred mcclient.TokenCredential, input *apis.ContainerVolumeMountDiskOverlay, _ *models.SDisk) error {
	return d.validateCommonCreateData(ctx, userCred, input)
}

func (d diskOverlayDir) validatePodCreateData(ctx context.Context, userCred mcclient.TokenCredential, input *apis.ContainerVolumeMountDiskOverlay, disk *api.DiskConfig) error {
	return d.validateCommonCreateData(ctx, userCred, input)
}

type diskOverlayImage struct{}

func newDiskOverlayImage() iDiskOverlay {
	return &diskOverlayImage{}
}

func (d diskOverlayImage) validateCreateData(ctx context.Context, userCred mcclient.TokenCredential, input *apis.ContainerVolumeMountDiskOverlay, diskObj *models.SDisk) error {
	if !input.UseDiskImage {
		return nil
	}
	if diskObj.TemplateId == "" {
		return httperrors.NewInputParameterError("disk %s must have template_id", diskObj.GetId())
	}
	return nil
}

func (d diskOverlayImage) validatePodCreateData(ctx context.Context, userCred mcclient.TokenCredential, input *apis.ContainerVolumeMountDiskOverlay, disk *api.DiskConfig) error {
	if !input.UseDiskImage {
		return nil
	}
	if disk.ImageId == "" {
		return httperrors.NewInputParameterError("disk %#v must have image_id", disk)
	}
	return nil
}
