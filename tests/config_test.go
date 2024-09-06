package videostore

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"go.viam.com/rdk/components/camera"
	_ "go.viam.com/rdk/components/camera/fake" // Register the fake camera model
	"go.viam.com/rdk/config"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/robot"
	robotimpl "go.viam.com/rdk/robot/impl"
	"go.viam.com/test"
)

const (
	moduleBinPath           = "/host/bin/linux-arm64/video-store"
	videoStoreComponentName = "video-store-1"
)

func setupViamServer(ctx context.Context, configStr string) (robot.Robot, error) {
	// setup viam server
	logger := logging.NewLogger("video-store-module")
	cfg, err := config.FromReader(ctx, "default.json", bytes.NewReader([]byte(configStr)), logger)
	if err != nil {
		logger.Error("failed to parse config", err)
		return nil, err
	}
	r, err := robotimpl.RobotFromConfig(ctx, cfg, logger)
	if err != nil {
		logger.Error("failed to create robot", err)
		return nil, err
	}
	return r, nil
}

func TestModuleConfiguration(t *testing.T) {
	// full config
	config1 := fmt.Sprintf(`
	{
		"components": [
			{
				"name": "video-store-1",
				"namespace": "rdk",
				"type": "camera",
				"model": "viam:video:storage",
				"attributes": {
					"camera": "fake-cam-1",
					"storage": {
						"size_gb": 10,
						"segment_seconds": 30,
						"upload_path": "/tmp",
						"storage_path": "/tmp"
					},
					"cam_props": {
						"width": 1920,
						"height": 1080,
						"framerate": 30
					},
					"video": {
						"codec": "h264",
						"bitrate": 1000000,
						"preset": "ultrafast",
						"format": "mp4"
					}
				},
				"depends_on": [
					"fake-cam-1"
				]
			},
			{
			"name": "fake-cam-1",
			"namespace": "rdk",
			"type": "camera",
			"model": "fake",
			"attributes": {}
			}
		],
		"modules": [
			{
				"type": "local",
				"name": "video-storage",
				"executable_path": "%s",
				"log_level": "debug"
			}
		]
	}`, moduleBinPath)

	// no camera specified
	config2 := fmt.Sprintf(`
    {
        "components": [
            {
                "name": "video-store-1",
                "namespace": "rdk",
                "type": "camera",
                "model": "viam:video:storage",
                "attributes": {
                    "storage": {
                        "size_gb": 10,
                        "segment_seconds": 30
                    }
                }
            }
        ],
        "modules": [
            {
                "type": "local",
                "name": "video-storage",
                "executable_path": "%s",
                "log_level": "debug"
            }
        ]
    }`, moduleBinPath)

	// no storage specified
	config3 := fmt.Sprintf(`
	{
		"components": [
			{
				"name": "video-store-1",
				"namespace": "rdk",
				"type": "camera",
				"model": "viam:video:storage",
				"attributes": {
					"camera": "fake-cam-1",
					"cam_props": {
						"width": 1920,
						"height": 1080,
						"framerate": 30
					},
					"video": {
						"codec": "h264",
						"bitrate": 1000000,
						"preset": "ultrafast",
						"format": "mp4"
					}
				},
				"depends_on": [
					"fake-cam-1"
				]
			},
			{
				"name": "fake-cam-1",
				"namespace": "rdk",
				"type": "camera",
				"model": "fake",
				"attributes": {}
			}
		],
		"modules": [
			{
				"type": "local",
				"name": "video-storage",
				"executable_path": "%s",
				"log_level": "debug"
			}
		]
	}`, moduleBinPath)

	// no size_gb specified
	config4 := fmt.Sprintf(`
	{
		"components": [
			{
				"name": "video-store-1",
				"namespace": "rdk",
				"type": "camera",
				"model": "viam:video:storage",
				"attributes": {
					"camera": "fake-cam-1",
					"storage": {
						"segment_seconds": 30,
						"upload_path": "/tmp/video-upload",
						"storage_path": "/tmp/video-storage"
					},
					"cam_props": {
						"width": 1920,
						"height": 1080,
						"framerate": 30
					},
					"video": {
						"codec": "h264",
						"bitrate": 1000000,
						"preset": "ultrafast",
						"format": "mp4"
					}
				},
				"depends_on": [
					"fake-cam-1"
				]
			},
			{
				"name": "fake-cam-1",
				"namespace": "rdk",
				"type": "camera",
				"model": "fake",
				"attributes": {}
			}
		],
		"modules": [
			{
				"type": "local",
				"name": "video-storage",
				"executable_path": "%s",
				"log_level": "debug"
			}
		]
	}`, moduleBinPath)

	// no cam_props specified
	config5 := fmt.Sprintf(`
	{
		"components": [
			{
				"name": "video-store-1",
				"namespace": "rdk",
				"type": "camera",
				"model": "viam:video:storage",
				"attributes": {
					"camera": "fake-cam-1",
					"storage": {
						"size_gb": 10,
						"segment_seconds": 30,
						"upload_path": "/tmp",
						"storage_path": "/tmp"
					},
					"video": {
						"codec": "h264",
						"bitrate": 1000000,
						"preset": "ultrafast",
						"format": "mp4"
					}
				},
				"depends_on": [
					"fake-cam-1"
				]
			},
			{
				"name": "fake-cam-1",
				"namespace": "rdk",
				"type": "camera",
				"model": "fake",
				"attributes": {}
			}
		],
		"modules": [
			{
				"type": "local",
				"name": "video-storage",
				"executable_path": "%s",
				"log_level": "debug"
			}
		]
	}`, moduleBinPath)

	t.Run("Valid Configuration Successful", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		_, err = camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
	})

	// no camera specified
	t.Run("Fails Configuration No Camera", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config2)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		_, err = camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldNotBeNil)
	})

	// no storage specified
	t.Run("Fails Configuration No Storage", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config3)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		_, err = camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldNotBeNil)
	})

	t.Run("Fails Configuration No SizeGB", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config4)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		_, err = camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldNotBeNil)
	})

	t.Run("Fails Configuration No CamProps", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config5)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		_, err = camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldNotBeNil)
	})
}