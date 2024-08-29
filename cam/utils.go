package videostore

/*
#include <libavutil/error.h>
#include <libavutil/opt.h>
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
*/
import "C"

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"
)

// ffmpegError returns a string representation of the ffmpeg error code.
func ffmpegError(ret C.int) string {
	var errbuf [256]C.char
	C.av_strerror(ret, &errbuf[0], 256)
	if errbuf[0] == 0 {
		return "unknown error"
	}
	return C.GoString(&errbuf[0])
}

// ffmppegLogLevel sets the log level for ffmpeg logger.
func ffmppegLogLevel(loglevel C.int) {
	// TODO(seanp): make sure log level is valid before setting
	C.av_log_set_level(loglevel)
}

// lookupLogID returns the log ID for the provided log level.
func lookupLogID(level string) C.int {
	switch level {
	case "error":
		return C.AV_LOG_ERROR
	case "warning":
		return C.AV_LOG_WARNING
	case "info":
		return C.AV_LOG_INFO
	case "debug":
		return C.AV_LOG_DEBUG
	default:
		return C.AV_LOG_INFO
	}
}

// lookupCodecID returns the codec ID for the provided codec.
func lookupCodecID(codec string) C.enum_AVCodecID {
	switch codec {
	case "h264":
		return C.AV_CODEC_ID_H264
	default:
		return C.AV_CODEC_ID_NONE
	}
}

// getHomeDir returns the home directory of the user.
func getHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// createDir creates a directory at the provided path if it does not exist.
func createDir(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err := os.MkdirAll(path, 0o755)
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

// getSortedFiles returns a list of files in the provided directory sorted by creation time.
func getSortedFiles(path string) ([]string, error) {
	files, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var filePaths []string
	for _, file := range files {
		filePaths = append(filePaths, filepath.Join(path, file.Name()))
	}
	sort.Slice(filePaths, func(i, j int) bool {
		timeI, errI := extractDateTime(filePaths[i])
		timeJ, errJ := extractDateTime(filePaths[j])
		if errI != nil || errJ != nil {
			return false
		}
		return timeI.Before(timeJ)
	})

	return filePaths, nil
}

// extractDateTime extracts the date and time from the file name.
func extractDateTime(filePath string) (time.Time, error) {
	baseName := filepath.Base(filePath)
	parts := strings.Split(baseName, "_")
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("invalid file name: %s", baseName)
	}
	datePart := parts[1]
	timePart := strings.TrimSuffix(parts[1], filepath.Ext(baseName))
	dateTimeStr := datePart + "_" + timePart
	dateTime, err := time.Parse("2006-01-02_15-04-05", dateTimeStr)
	if err != nil {
		return time.Time{}, err
	}
	return dateTime, nil
}

// copyFile copies a file from the source to the destination.
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	destinationFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destinationFile.Close()
	_, err = io.Copy(destinationFile, sourceFile)
	if err != nil {
		return err
	}
	err = destinationFile.Sync()
	if err != nil {
		return err
	}
	return nil
}

// fetchCompName
func fetchCompName(resourceName string) string {
	parts := strings.Split(resourceName, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "unknown"
}

// concatFiles concatenates video files in the provided list into a single file.
func concatFiles(files []string, output string) error {
	// create concat.txt file
	fmt.Println("Creating concat.txt file")
	concatFile, err := os.Create("/home/viam/.viam/concat.txt")
	if err != nil {
		return err
	}
	defer concatFile.Close()
	for _, file := range files {
		_, err := concatFile.WriteString(fmt.Sprintf("file '%s'\n", file))
		if err != nil {
			return err
		}
	}

	// open the concat format context
	filename := C.CString("/home/viam/.viam/concat.txt")
	defer C.free(unsafe.Pointer(filename))
	var inputCtx *C.AVFormatContext
	inputFormat := C.av_find_input_format(C.CString("concat"))
	if inputFormat == nil {
		return fmt.Errorf("failed to find input format")
	}

	// we need this to allow absolute paths
	var options *C.AVDictionary
	C.av_dict_set(&options, C.CString("safe"), C.CString("0"), 0)

	ret := C.avformat_open_input(&inputCtx, filename, inputFormat, &options)
	if ret < 0 {
		return fmt.Errorf("failed to open input context: %s", ffmpegError(ret))
	}

	// find the stream info
	ret = C.avformat_find_stream_info(inputCtx, nil)
	if ret < 0 {
		return fmt.Errorf("failed to find stream info: %s", ffmpegError(ret))
	}

	// create the output format context
	outputFile := C.CString(output)
	var outputCtx *C.AVFormatContext
	ret = C.avformat_alloc_output_context2(&outputCtx, nil, nil, outputFile)
	if ret < 0 {
		return fmt.Errorf("failed to allocate output context: %s", ffmpegError(ret))
	}

	// copy the streams from the input to the output
	fmt.Println("Copying streams")
	for i := 0; i < int(inputCtx.nb_streams); i++ {
		// pointer arithmetic to get the stream
		inStream := *(**C.AVStream)(unsafe.Pointer(uintptr(unsafe.Pointer(inputCtx.streams)) + uintptr(i)*unsafe.Sizeof(inputCtx.streams)))
		outStream := C.avformat_new_stream(outputCtx, nil)
		if outStream == nil {
			return fmt.Errorf("failed to allocate stream")
		}

		// copy codec parameters from input stream to output stream
		ret := C.avcodec_parameters_copy(outStream.codecpar, inStream.codecpar)
		if ret < 0 {
			return fmt.Errorf("failed to copy codec parameters: %s", ffmpegError(ret))
		}

		// let ffmpeg handle the codec tag for us
		outStream.codecpar.codec_tag = 0
	}

	// open the output file
	ret = C.avio_open(&outputCtx.pb, outputFile, C.AVIO_FLAG_WRITE)
	if ret < 0 {
		return fmt.Errorf("failed to open output file: %s", ffmpegError(ret))
	}

	// write the header
	ret = C.avformat_write_header(outputCtx, nil)
	if ret < 0 {
		return fmt.Errorf("failed to write header: %s", ffmpegError(ret))
	}

	// read the packets from the input and write them to the output
	fmt.Println("Writing packets")
	packet := C.av_packet_alloc()
	for {
		ret := C.av_read_frame(inputCtx, packet)
		if ret == C.AVERROR_EOF {
			break
		}
		if ret < 0 {
			return fmt.Errorf("failed to read frame: %s", ffmpegError(ret))
		}

		// adjust the PTS and DTS
		// packet.pts = C.av_rescale_q_rnd(packet.pts, inputCtx.streams[packet.stream_index].time_base, outputCtx.streams[packet.stream_index].time_base, C.AV_ROUND_NEAR_INF|C.AV_ROUND_PASS_MINMAX)
		// packet.dts = C.av_rescale_q_rnd(packet.dts, inputCtx.streams[packet.stream_index].time_base, outputCtx.streams[packet.stream_index].time_base, C.AV_ROUND_NEAR_INF|C.AV_ROUND_PASS_MINMAX)
		// packet.duration = C.av_rescale_q(packet.duration, inputCtx.streams[packet.stream_index].time_base, outputCtx.streams[packet.stream_index].time_base)
		// Adjust the PTS and DTS
		packet.pts = C.av_rescale_q_rnd(packet.pts, (*C.AVStream)(unsafe.Pointer(uintptr(unsafe.Pointer(inputCtx.streams))+uintptr(packet.stream_index)*unsafe.Sizeof(uintptr(0)))).time_base, (*C.AVStream)(unsafe.Pointer(uintptr(unsafe.Pointer(outputCtx.streams))+uintptr(packet.stream_index)*unsafe.Sizeof(uintptr(0)))).time_base, C.AV_ROUND_NEAR_INF|C.AV_ROUND_PASS_MINMAX)

		packet.dts = C.av_rescale_q_rnd(packet.dts, (*C.AVStream)(unsafe.Pointer(uintptr(unsafe.Pointer(inputCtx.streams))+uintptr(packet.stream_index)*unsafe.Sizeof(uintptr(0)))).time_base, (*C.AVStream)(unsafe.Pointer(uintptr(unsafe.Pointer(outputCtx.streams))+uintptr(packet.stream_index)*unsafe.Sizeof(uintptr(0)))).time_base, C.AV_ROUND_NEAR_INF|C.AV_ROUND_PASS_MINMAX)

		packet.duration = C.av_rescale_q(packet.duration, (*C.AVStream)(unsafe.Pointer(uintptr(unsafe.Pointer(inputCtx.streams))+uintptr(packet.stream_index)*unsafe.Sizeof(uintptr(0)))).time_base, (*C.AVStream)(unsafe.Pointer(uintptr(unsafe.Pointer(outputCtx.streams))+uintptr(packet.stream_index)*unsafe.Sizeof(uintptr(0)))).time_base)

		packet.pos = -1

		// write the packet
		ret = C.av_interleaved_write_frame(outputCtx, packet)
		if ret < 0 {
			return fmt.Errorf("failed to write frame: %s", ffmpegError(ret))
		}
	}

	// write the trailer
	ret = C.av_write_trailer(outputCtx)
	if ret < 0 {
		return fmt.Errorf("failed to write trailer: %s", ffmpegError(ret))
	}

	// cleanup
	C.avformat_close_input(&inputCtx)
	C.avformat_free_context(outputCtx)
	C.av_packet_free(&packet)

	return nil
}
