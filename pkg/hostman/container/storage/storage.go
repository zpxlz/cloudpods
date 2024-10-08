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

package storage

import (
	"fmt"

	"yunion.io/x/onecloud/pkg/util/mountutils"
)

var (
	drivers = make(map[StorageType]IContainerStorage)
)

type StorageType string

const (
	STORAGE_TYPE_LOCAL_RAW   StorageType = "local_raw"
	STORAGE_TYPE_LOCAL_QCOW2 StorageType = "local_qcow2"
)

type IContainerStorage interface {
	GetType() StorageType
	CheckConnect(diskPath string) (string, bool, error)
	ConnectDisk(diskPath string) (string, error)
	DisconnectDisk(diskPath string, mountPoint string) error
}

func GetDriver(t StorageType) IContainerStorage {
	return drivers[t]
}

func RegisterDriver(drv IContainerStorage) {
	_, ok := drivers[drv.GetType()]
	if ok {
		panic(fmt.Sprintf("driver %s already registered", drv.GetType()))
	}
	drivers[drv.GetType()] = drv
}

func Mount(devPath string, mountPoint string, fsType string) error {
	return mountutils.Mount(devPath, mountPoint, fsType)
}

func Unmount(devPath string) error {
	return mountutils.Unmount(devPath)
}
