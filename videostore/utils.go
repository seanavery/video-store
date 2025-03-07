package videostore

/*
#include "utils.h"
#include <libavutil/error.h>
#include <libavutil/opt.h>
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"

	"go.viam.com/rdk/logging"
)

// SetLibAVLogLevel sets the libav log level.
// this is global for the entire OS process.
// valid inputs are "info", "warn", "error", "debug"
// https://www.ffmpeg.org/doxygen/2.5/group__lavu__log__constants.html
func SetLibAVLogLevel(level string) {
	ffmppegLogLevel(lookupLogID(level))
}

type codecType int

const (
	// CodecUnknown represents an unknown codec type.
	codecUnknown codecType = iota
	// CodecH264 represents the H.264 codec type.
	codecH264
	// CodecH265 represents the H.265 codec type.
	codecH265
)

func (c codecType) String() string {
	switch c {
	case codecH264:
		return "h264"
	case codecH265:
		return "h265"
	default:
		return "unknown"
	}
}

func parseCodecType(codec string) codecType {
	switch codec {
	case "h264":
		return codecH264
	case "h265":
		return codecH265
	default:
		return codecUnknown
	}
}

// lookupCodecIDByType returns the FFmpeg codec ID for the provided codec type.
func lookupCodecIDByType(codec codecType) C.enum_AVCodecID {
	switch codec {
	case codecH264:
		return C.AV_CODEC_ID_H264
	case codecH265:
		return C.AV_CODEC_ID_H265
	default:
		return C.AV_CODEC_ID_NONE
	}
}

// lookupCodecID returns the codec ID for the provided codec.
func lookupCodecTypeByID(codecID C.enum_AVCodecID) codecType {
	switch codecID {
	case C.AV_CODEC_ID_H264:
		return codecH264
	case C.AV_CODEC_ID_H265:
		return codecH265
	default:
		return codecUnknown
	}
}

// ffmpegError returns a string representation of the ffmpeg error code.
func ffmpegError(ret C.int) string {
	const errbufSize = 256
	var errbuf [errbufSize]C.char
	C.av_strerror(ret, &errbuf[0], errbufSize)
	if errbuf[0] == 0 {
		return "unknown ffmpeg error"
	}
	return C.GoString(&errbuf[0])
}

// ffmppegLogLevel sets the log level for ffmpeg logger.
func ffmppegLogLevel(loglevel C.int) {
	C.av_log_set_level(loglevel)
}

// lookupLogID returns the log ID for the provided log level.
func lookupLogID(level string) C.int {
	switch level {
	case "error":
		return C.AV_LOG_ERROR
	case "warn":
		return C.AV_LOG_WARNING
	case "info":
		return C.AV_LOG_INFO
	case "debug":
		return C.AV_LOG_DEBUG
	default:
		return C.AV_LOG_INFO
	}
}

// createDir creates a directory at the provided path if it does not exist.
func createDir(path string) error {
	const dirPermissions = 0o755
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err := os.MkdirAll(path, dirPermissions)
		if err != nil {
			return err
		}
	}
	return nil
}

// readVideoFile takes in a path to mp4 file and returns bytes of the file.
func readVideoFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// getDirectorySize returns the size of a directory in bytes.
func getDirectorySize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return size, err
}

// getFileSize returns the size of a file in bytes.
func getFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// getSortedFiles returns a list of files in the provided directory sorted by creation time.
func getSortedFiles(path string) ([]string, error) {
	files, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var validFilePaths []string
	for _, file := range files {
		filePath := filepath.Join(path, file.Name())
		_, err := extractDateTimeFromFilename(filePath)
		if err == nil {
			validFilePaths = append(validFilePaths, filePath)
		}
	}
	sort.Slice(validFilePaths, func(i, j int) bool {
		timeI, errI := extractDateTimeFromFilename(validFilePaths[i])
		timeJ, errJ := extractDateTimeFromFilename(validFilePaths[j])
		if errI != nil || errJ != nil {
			return false
		}
		return timeI.Before(timeJ)
	})

	return validFilePaths, nil
}

// extractDateTimeFromFilename extracts the date and time from the filename.
func extractDateTimeFromFilename(filePath string) (time.Time, error) {
	const minParts = 2
	baseName := filepath.Base(filePath)
	parts := strings.Split(baseName, "_")
	if len(parts) < minParts {
		return time.Time{}, fmt.Errorf("invalid file name: %s", baseName)
	}
	datePart := parts[0]
	timePart := strings.TrimSuffix(parts[1], filepath.Ext(parts[1]))
	dateTimeStr := datePart + "_" + timePart
	return ParseDateTimeString(dateTimeStr)
}

// ParseDateTimeString parses a date and time string in the format "2006-01-02_15-04-05".
// Returns a time.Time object and an error if the string is not in the correct format.
func ParseDateTimeString(datetime string) (time.Time, error) {
	dateTime, err := time.Parse("2006-01-02_15-04-05", datetime)
	if err != nil {
		return time.Time{}, err
	}
	return dateTime, nil
}

func formatDateTimeToString(dateTime time.Time) string {
	return dateTime.Format("2006-01-02_15-04-05")
}

// matchStorageToRange returns a list of files that fall within the provided time range.
// Includes trimming video files to the time range if they overlap.
func matchStorageToRange(files []string, start, end time.Time, logger logging.Logger) []string {
	var matchedFiles []string
	for _, file := range files {
		dateTime, err := extractDateTimeFromFilename(file)
		if err != nil {
			logger.Debugf("failed to extract datetime from filename: %s, error: %v", file, err)
			continue
		}
		duration, _, _, _, err := getVideoInfo(file)
		if err != nil {
			logger.Debugf("failed to get video duration for file: %s, error: %v", file, err)
			continue
		}
		fileEndTime := dateTime.Add(duration)
		// Check if the file's time range intersects with [start, end)
		if dateTime.Before(end) && fileEndTime.After(start) {
			var inpoint, outpoint float64
			inpointSet := false
			outpointSet := false
			// Calculate inpoint if the file starts before the 'start' time and overlaps
			if dateTime.Before(start) {
				inpoint = start.Sub(dateTime).Seconds()
				inpointSet = true
			}
			// Calculate outpoint if the file ends after the 'end' time
			if fileEndTime.After(end) {
				outpoint = end.Sub(dateTime).Seconds()
				outpointSet = true
			}
			matchedFiles = append(matchedFiles, fmt.Sprintf("file '%s'", file))
			if inpointSet {
				matchedFiles = append(matchedFiles, fmt.Sprintf("inpoint %.2f", inpoint))
			}
			if outpointSet && outpoint < duration.Seconds() {
				// Only include outpoint if it's less than the full duration
				matchedFiles = append(matchedFiles, fmt.Sprintf("outpoint %.2f", outpoint))
			}
		}
	}

	return matchedFiles
}

// generateOutputFilename generates the output filename for the video file.
func generateOutputFilePath(camName, fromStr, metadata, path string) string {
	var outputFilename string
	if metadata == "" {
		outputFilename = fmt.Sprintf("%s_%s.%s", camName, fromStr, defaultVideoFormat)
	} else {
		outputFilename = fmt.Sprintf("%s_%s_%s.%s", camName, fromStr, metadata, defaultVideoFormat)
	}
	return filepath.Join(path, outputFilename)
}

// validateTimeRange validates the start and end time range against storage files.
// Extracts the start timestamp of the oldest file and the start of the most recent file.
// Since the most recent segment file is still being written to by the segmenter
// we do not want to include it in the time range.
func validateTimeRange(files []string, start, end time.Time) error {
	if len(files) == 0 {
		return errors.New("no storage files found")
	}
	oldestFileStart, err := extractDateTimeFromFilename(files[0])
	if err != nil {
		return err
	}
	newestFileStart, err := extractDateTimeFromFilename(files[len(files)-1])
	if err != nil {
		return err
	}
	if start.Before(oldestFileStart) || end.After(newestFileStart) {
		return errors.New("time range is outside of storage range")
	}
	return nil
}

// // getVideoDuration returns the duration of the video file in seconds.
// func getVideoDuration(filePath string) (time.Duration, error) {
// 	cFilePath := C.CString(filePath)
// 	defer C.free(unsafe.Pointer(cFilePath))

// 	var duration C.int64_t
// 	ret := C.get_video_duration(&duration, cFilePath)
// 	switch ret {
// 	case C.VIDEO_STORE_DURATION_RESP_OK:
// 		// Convert duration from AV_TIME_BASE units to time.Duration
// 		return time.Duration(duration) * time.Microsecond, nil
// 	case C.VIDEO_STORE_DURATION_RESP_ERROR:
// 		return 0, fmt.Errorf("failed to get video duration for file: %s", filePath)
// 	default:
// 		return 0, fmt.Errorf("failed to get video duration for file: %s with error: %s", filePath, ffmpegError(ret))
// 	}
// }

// getVideoInfo calls the C function get_video_info to retrieve duration, width, height, and codec.
func getVideoInfo(filePath string) (time.Duration, int, int, string, error) {
	cFilePath := C.CString(filePath)
	defer C.free(unsafe.Pointer(cFilePath))

	var cDuration C.int64_t
	var cWidth, cHeight C.int
	var cCodec [C.VIDEO_STORE_CODEC_NAME_LEN]C.char

	ret := C.get_video_info(&cDuration, &cWidth, &cHeight, &cCodec[0], cFilePath)
	switch ret {
	case C.VIDEO_STORE_VIDEO_INFO_RESP_OK:
		// Convert and return
		duration := time.Duration(cDuration) * time.Microsecond
		width := int(cWidth)
		height := int(cHeight)
		codec := C.GoString(&cCodec[0])
		return duration, width, height, codec, nil
	case C.VIDEO_STORE_VIDEO_INFO_RESP_ERROR:
		return 0, 0, 0, "", fmt.Errorf("failed to get video info for file: %s", filePath)
	default:
		return 0, 0, 0, "", fmt.Errorf("failed to get video info for file: %s with error: %s",
			filePath, ffmpegError(ret))
	}
}
