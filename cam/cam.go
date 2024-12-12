// Package videostore contains the implementation of the video storage camera component.
package videostore

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/gostream"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/pointcloud"
	"go.viam.com/rdk/resource"
	"go.viam.com/utils"
)

// Model is the model for the video storage camera component.
var Model = resource.ModelNamespace("viam").WithFamily("video").WithModel("storage")

const (
	// Default values for the video storage camera component.
	defaultSegmentSeconds = 30 // seconds
	defaultStorageSize    = 10 // GB
	defaultVideoCodec     = codecH264
	defaultVideoBitrate   = 1000000
	defaultVideoPreset    = "medium"
	defaultVideoFormat    = "mp4"
	defaultUploadPath     = ".viam/capture/video-upload"
	defaultStoragePath    = ".viam/video-storage"
	defaultLogLevel       = "error"

	maxGRPCSize     = 1024 * 1024 * 32 // bytes
	deleterInterval = 10               // minutes
	retryInterval   = 1                // seconds
	asyncTimeout    = 60               // seconds
	tempPath        = "/tmp"
)

type videostore struct {
	resource.AlwaysRebuild

	name   resource.Name
	conf   *Config
	logger logging.Logger

	cam camera.Camera
	// latestFrame atomic.Pointer[image.Image]
	latestFrame atomic.Pointer[[]byte]
	workers     *utils.StoppableWorkers

	enc  *encoder
	seg  *segmenter
	conc *concater

	storagePath string
	uploadPath  string
}

type storage struct {
	SegmentSeconds int    `json:"segment_seconds,omitempty"`
	SizeGB         int    `json:"size_gb"`
	UploadPath     string `json:"upload_path,omitempty"`
	StoragePath    string `json:"storage_path,omitempty"`
}

type video struct {
	Codec   string `json:"codec,omitempty"`
	Bitrate int    `json:"bitrate,omitempty"`
	Preset  string `json:"preset,omitempty"`
	Format  string `json:"format,omitempty"`
}

type cameraProperties struct {
	Width     int `json:"width"`
	Height    int `json:"height"`
	Framerate int `json:"framerate"`
}

// Config is the configuration for the video storage camera component.
type Config struct {
	Camera  string  `json:"camera"`
	Sync    string  `json:"sync"`
	Storage storage `json:"storage"`
	Video   video   `json:"video,omitempty"`

	// TODO(seanp): Remove once camera properties are returned from camera component.
	Properties cameraProperties `json:"cam_props"`
}

// Validate validates the configuration for the video storage camera component.
func (cfg *Config) Validate(path string) ([]string, error) {
	if cfg.Camera == "" {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "camera")
	}
	if cfg.Storage == (storage{}) {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "storage")
	}
	if cfg.Storage.SizeGB == 0 {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "size_gb")
	}
	if cfg.Sync == "" {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "sync")
	}
	// TODO(seanp): Remove once camera properties are returned from camera component.
	if cfg.Properties == (cameraProperties{}) {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "cam_props")
	}

	return []string{cfg.Camera}, nil
}

func init() {
	resource.RegisterComponent(
		camera.API,
		Model,
		resource.Registration[camera.Camera, *Config]{
			Constructor: newvideostore,
		})
}

func newvideostore(
	_ context.Context,
	deps resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (camera.Camera, error) {
	newConf, err := resource.NativeConfig[*Config](conf)
	if err != nil {
		return nil, err
	}

	vs := &videostore{
		name:   conf.ResourceName(),
		conf:   newConf,
		logger: logger,
	}

	// Source camera that provides the frames to be processed.
	vs.cam, err = camera.FromDependencies(deps, newConf.Camera)
	if err != nil {
		return nil, err
	}

	// TODO(seanp): make this configurable
	logLevel := lookupLogID(defaultLogLevel)
	ffmppegLogLevel(logLevel)

	// Create encoder to handle encoding of frames.
	// TODO(seanp): Forcing h264 for now until h265 is supported.
	codec := defaultVideoCodec
	bitrate := defaultVideoBitrate
	preset := defaultVideoPreset
	format := defaultVideoFormat
	if newConf.Video.Bitrate != 0 {
		bitrate = newConf.Video.Bitrate
	}
	if newConf.Video.Preset != "" {
		preset = newConf.Video.Preset
	}
	if newConf.Video.Format != "" {
		format = newConf.Video.Format
	}
	vs.enc, err = newEncoder(
		logger,
		codec,
		bitrate,
		preset,
		newConf.Properties.Width,
		newConf.Properties.Height,
		newConf.Properties.Framerate,
	)
	if err != nil {
		return nil, err
	}

	// Create segmenter to handle segmentation of video stream into clips.
	sizeGB := newConf.Storage.SizeGB
	segmentSeconds := defaultSegmentSeconds
	uploadPath := filepath.Join(getHomeDir(), defaultUploadPath, vs.name.Name)
	storagePath := filepath.Join(getHomeDir(), defaultStoragePath, vs.name.Name)
	if newConf.Storage.SegmentSeconds != 0 {
		segmentSeconds = newConf.Storage.SegmentSeconds
	}
	if newConf.Storage.UploadPath != "" {
		uploadPath = newConf.Storage.UploadPath
	}
	if newConf.Storage.StoragePath != "" {
		storagePath = newConf.Storage.StoragePath
	}

	// Check for data_manager service dependency.
	// TODO(seanp): Check custom_sync_paths if not using default upload_path in config.
	syncFound := false
	for key, dep := range deps {
		if key.Name == newConf.Sync {
			if dep.Name().API.Type.String() != "rdk:service" {
				return nil, fmt.Errorf("sync service %s is not a service", newConf.Sync)
			}
			if dep.Name().API.SubtypeName != "data_manager" {
				return nil, fmt.Errorf("sync service %s is not a data_manager service", newConf.Sync)
			}
			logger.Debugf("found sync service: %s", key.Name)
			syncFound = true
			break
		}
	}
	if !syncFound {
		return nil, fmt.Errorf("sync service %s not found", newConf.Sync)
	}

	vs.storagePath = storagePath
	vs.seg, err = newSegmenter(
		logger,
		vs.enc,
		sizeGB,
		segmentSeconds,
		storagePath,
		format,
	)
	if err != nil {
		return nil, err
	}

	// Create concater to handle concatenation of video clips when requested.
	vs.uploadPath = uploadPath
	err = createDir(vs.uploadPath)
	if err != nil {
		return nil, err
	}
	vs.conc, err = newConcater(
		logger,
		vs.storagePath,
		vs.uploadPath,
		segmentSeconds,
	)
	if err != nil {
		return nil, err
	}

	// Start workers to process frames and clean up storage.
	vs.workers = utils.NewBackgroundStoppableWorkers(vs.fetchFrames, vs.processFrames, vs.deleter)

	return vs, nil
}

func (vs *videostore) Name() resource.Name {
	return vs.name
}

// DoCommand processes the commands for the video storage camera component.
func (vs *videostore) DoCommand(_ context.Context, command map[string]interface{}) (map[string]interface{}, error) {
	cmd, ok := command["command"].(string)
	if !ok {
		return nil, errors.New("invalid command type")
	}

	switch cmd {
	// Save command is used to concatenate video clips between the given timestamps.
	// The concatenated video file is then uploaded to the cloud the upload path.
	// The response contains the name of the uploaded file.
	case "save":
		vs.logger.Debug("save command received")
		from, to, metadata, async, err := validateSaveCommand(command)
		if err != nil {
			return nil, err
		}
		uploadFilePath := generateOutputFilePath(vs.name.Name, formatDateTimeToString(from), metadata, vs.uploadPath)
		uploadFileName := filepath.Base(uploadFilePath)
		switch async {
		case true:
			vs.logger.Debug("running save command asynchronously")
			vs.workers.Add(func(ctx context.Context) {
				vs.asyncSave(ctx, from, to, uploadFilePath)
			})
			return map[string]interface{}{
				"command":  "save",
				"filename": uploadFileName,
				"status":   "async",
			}, nil
		default:
			err = vs.conc.concat(from, to, uploadFilePath)
			if err != nil {
				vs.logger.Error("failed to concat files ", err)
				return nil, err
			}
			return map[string]interface{}{
				"command":  "save",
				"filename": uploadFileName,
			}, nil
		}
	case "fetch":
		vs.logger.Debug("fetch command received")
		from, to, err := validateFetchCommand(command)
		if err != nil {
			return nil, err
		}
		fetchFilePath := generateOutputFilePath(vs.name.Name, formatDateTimeToString(from), "", tempPath)
		err = vs.conc.concat(from, to, fetchFilePath)
		if err != nil {
			vs.logger.Error("failed to concat files ", err)
			return nil, err
		}
		videoSize, err := getFileSize(fetchFilePath)
		if err != nil {
			return nil, err
		}
		if videoSize > maxGRPCSize {
			return nil, errors.New("video file size exceeds max grpc size")
		}
		videoBytes, err := readVideoFile(fetchFilePath)
		if err != nil {
			return nil, err
		}
		vs.logger.Debug("video bytes: ", len(videoBytes))
		// TODO(seanp): Do we need to encode the video bytes to base64?
		videoBytesBase64 := base64.StdEncoding.EncodeToString(videoBytes)
		return map[string]interface{}{
			"command": "fetch",
			"video":   videoBytesBase64,
		}, nil
	default:
		return nil, errors.New("invalid command")
	}
}

func (vs *videostore) Properties(_ context.Context) (camera.Properties, error) {
	return camera.Properties{}, nil
}

// fetchFrames reads frames from the camera and stores the decoded image
// in the latestFrame atomic pointer. This routine runs as fast as possible
// to keep the latest frame up to date.
func (vs *videostore) fetchFrames(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		// bytes, _, err := vs.cam.Image(ctx, , nil)
		bytes, _, err := vs.cam.Image(ctx, "", map[string]interface{}{})
		if err != nil {
			vs.logger.Warn("failed to get frame from camera", err)
			time.Sleep(retryInterval * time.Second)
			continue
		}
		vs.latestFrame.Store(&bytes)
		// frame, err := camera.DecodeImageFromCamera(ctx, rutils.MimeTypeJPEG, nil, vs.cam)
		// if err != nil {
		// 	vs.logger.Warn("failed to get frame from camera", err)
		// 	time.Sleep(retryInterval * time.Second)
		// 	continue
		// }
		// lazyImage, ok := frame.(*rimage.LazyEncodedImage)
		// if !ok {
		// 	vs.logger.Error("frame is not of type *rimage.LazyEncodedImage")
		// 	return
		// }
		// decodedImage := lazyImage.DecodedImage()

		// vs.latestFrame.Store(&decodedImage)
	}
}

// processFrames grabs the latest frame, encodes, and writes to the segmenter
// which chunks video stream into clip files inside the storage directory.
func (vs *videostore) processFrames(ctx context.Context) {
	frameInterval := time.Second / time.Duration(vs.conf.Properties.Framerate)
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			latestFrame := vs.latestFrame.Load()
			if latestFrame == nil {
				vs.logger.Debug("latest frame is not available yet")
				continue
			}
			encoded, pts, dts, err := vs.enc.encode(*latestFrame)
			if err != nil {
				vs.logger.Error("failed to encode frame", err)
				return
			}
			err = vs.seg.writeEncodedFrame(encoded, pts, dts)
			if err != nil {
				vs.logger.Error("failed to segment frame", err)
				return
			}
		}
	}
}

// deleter is a go routine that cleans up old clips if storage is full. Runs on interval
// and deletes the oldest clip until the storage size is below the configured max.
func (vs *videostore) deleter(ctx context.Context) {
	ticker := time.NewTicker(deleterInterval * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Perform the deletion of the oldest clip
			err := vs.seg.cleanupStorage()
			if err != nil {
				vs.logger.Error("failed to clean up storage", err)
				continue
			}
		}
	}
}

// asyncSave command will run the concat operation in the background.
// It waits for the segment duration before running to ensure the last segment
// is written to storage before concatenation.
// TODO: (seanp) Optimize this to immediately run as soon as the current segment is completed.
func (vs *videostore) asyncSave(ctx context.Context, from, to time.Time, path string) {
	totalTimeout := time.Duration(asyncTimeout)*time.Second + vs.conc.segmentDur
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	timer := time.NewTimer(vs.conc.segmentDur)
	defer timer.Stop()
	select {
	case <-timer.C:
		vs.logger.Debugf("executing concat for %s", path)
		err := vs.conc.concat(from, to, path)
		if err != nil {
			vs.logger.Error("failed to concat files ", err)
		}
		return
	case <-ctx.Done():
		vs.logger.Error("asyncSave operation cancelled or timed out")
		return
	}
}

// Close closes the video storage camera component.
func (vs *videostore) Close(_ context.Context) error {
	vs.workers.Stop()
	vs.enc.close()
	vs.seg.close()
	return nil
}

// Unimplemented methods for the video storage camera component.
func (vs *videostore) Stream(_ context.Context, _ ...gostream.ErrorHandler) (gostream.VideoStream, error) {
	return nil, errors.New("not implemented")
}

func (vs *videostore) Image(_ context.Context, _ string, _ map[string]interface{}) ([]byte, camera.ImageMetadata, error) {
	return nil, camera.ImageMetadata{}, errors.New("not implemented")
}

func (vs *videostore) Images(_ context.Context) ([]camera.NamedImage, resource.ResponseMetadata, error) {
	return nil, resource.ResponseMetadata{}, errors.New("not implemented")
}

func (vs *videostore) NextPointCloud(_ context.Context) (pointcloud.PointCloud, error) {
	return nil, errors.New("not implemented")
}
