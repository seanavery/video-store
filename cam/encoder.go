package videostore

/*
#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libswscale/swscale.h>
#include <libavutil/error.h>
#include <libavutil/opt.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"reflect"
	"unsafe"

	"go.viam.com/rdk/logging"
)

const (
	subsampleFactor = 2
)

type encoder struct {
	logger     logging.Logger
	codecCtx   *C.AVCodecContext
	srcFrame   *C.AVFrame
	dstFrame   *C.AVFrame
	frameCount int64
	width      int
	height     int
}

func newEncoder(
	logger logging.Logger,
	videoCodec codecType,
	bitrate int,
	preset string,
	width int,
	height int,
	framerate int,
) (*encoder, error) {
	enc := &encoder{
		logger:     logger,
		width:      width,
		height:     height,
		frameCount: 0,
	}
	codecID := lookupCodecIDByType(videoCodec)
	codec := C.avcodec_find_encoder(codecID)
	if codec == nil {
		return nil, errors.New("codec not found")
	}

	enc.codecCtx = C.avcodec_alloc_context3(codec)
	if enc.codecCtx == nil {
		return nil, errors.New("failed to allocate codec context")
	}

	enc.codecCtx.bit_rate = C.int64_t(bitrate)
	// enc.codecCtx.pix_fmt = C.AV_PIX_FMT_YUV422P
	// enc.codecCtx.pix_fmt = C.AV_PIX_FMT_YUYV422
	enc.codecCtx.pix_fmt = C.AV_PIX_FMT_YUV420P
	enc.codecCtx.time_base = C.AVRational{num: 1, den: C.int(framerate)}
	enc.codecCtx.width = C.int(width)
	enc.codecCtx.height = C.int(height)

	// TODO(seanp): Do we want b frames? This could make it more complicated to split clips.
	enc.codecCtx.max_b_frames = 0
	presetCStr := C.CString(preset)
	tuneCStr := C.CString("zerolatency")
	defer C.free(unsafe.Pointer(presetCStr))
	defer C.free(unsafe.Pointer(tuneCStr))

	// The user can set the preset and tune for the encoder. This affects the
	// encoding speed and quality. See https://trac.ffmpeg.org/wiki/Encode/H.264
	// for more information.
	var opts *C.AVDictionary
	defer C.av_dict_free(&opts)
	ret := C.av_dict_set(&opts, C.CString("preset"), presetCStr, 0)
	if ret < 0 {
		return nil, fmt.Errorf("av_dict_set failed: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("tune"), tuneCStr, 0)
	if ret < 0 {
		return nil, fmt.Errorf("av_dict_set failed: %s", ffmpegError(ret))
	}

	ret = C.avcodec_open2(enc.codecCtx, codec, &opts)
	if ret < 0 {
		return nil, fmt.Errorf("avcodec_open2: %s", ffmpegError(ret))
	}

	// srcFrame will hold the raw YUYV422 data
	srcFrame := C.av_frame_alloc()
	if srcFrame == nil {
		C.avcodec_close(enc.codecCtx)
		return nil, errors.New("could not allocate source frame")
	}
	srcFrame.width = enc.codecCtx.width
	srcFrame.height = enc.codecCtx.height
	// srcFrame.format = C.int(enc.codecCtx.pix_fmt)
	srcFrame.format = C.int(C.AV_PIX_FMT_YUYV422)
	enc.srcFrame = srcFrame

	// dstFrame will hold the YUV420p data
	dstFrame := C.av_frame_alloc()
	if dstFrame == nil {
		C.av_frame_free(&srcFrame)
		C.avcodec_close(enc.codecCtx)
		return nil, errors.New("could not allocate destination frame")
	}
	dstFrame.width = enc.codecCtx.width
	dstFrame.height = enc.codecCtx.height
	dstFrame.format = C.int(C.AV_PIX_FMT_YUV420P)
	enc.dstFrame = dstFrame

	return enc, nil
}

// encode encodes the given frame and returns the encoded data
// in bytes along with the PTS and DTS timestamps.
// PTS is calculated based on the frame count and source framerate.
// If the polling loop is not running at the source framerate, the
// PTS will lag behind actual run time.
// func (e *encoder) encode(frame image.Image) ([]byte, int64, int64, error) {
func (e *encoder) encode(frame []byte) ([]byte, int64, int64, error) {
	// We should no longer need to do this conversion since we can now directly pipe the YUV data
	// yuv, err := imageToYUV422(frame)
	// if err != nil {
	// 	return nil, 0, 0, err
	// }

	// ySize := frame.Bounds().Dx() * frame.Bounds().Dy()
	// uSize := (frame.Bounds().Dx() / subsampleFactor) * frame.Bounds().Dy()
	// vSize := (frame.Bounds().Dx() / subsampleFactor) * frame.Bounds().Dy()
	// yPlane := C.CBytes(yuv[:ySize])
	// uPlane := C.CBytes(yuv[ySize : ySize+uSize])
	// vPlane := C.CBytes(yuv[ySize+uSize : ySize+uSize+vSize])
	// defer C.free(yPlane)
	// defer C.free(uPlane)
	// defer C.free(vPlane)
	// e.srcFrame.data[0] = (*C.uint8_t)(yPlane)
	// e.srcFrame.data[1] = (*C.uint8_t)(uPlane)
	// e.srcFrame.data[2] = (*C.uint8_t)(vPlane)
	// e.srcFrame.linesize[0] = C.int(frame.Bounds().Dx())
	// e.srcFrame.linesize[1] = C.int(frame.Bounds().Dx() / subsampleFactor)
	// e.srcFrame.linesize[2] = C.int(frame.Bounds().Dx() / subsampleFactor)

	yuyv := C.CBytes(frame)
	defer C.free(yuyv)

	e.srcFrame.data[0] = (*C.uint8_t)(yuyv)
	e.srcFrame.linesize[0] = C.int(e.width * 2) // Each pixel is 2 bytes in YUYV422 format

	// convert the frame to YUV420p format
	if err := e.frameToYUV420p(); err != nil {
		return nil, 0, 0, err
	}

	// Both PTS and DTS times are equal frameCount multiplied by the time_base.
	// This assumes that the processFrame routine is running at the source framerate.
	// TODO(seanp): What happens to playback if frame is dropped?
	// e.srcFrame.pts = C.int64_t(e.frameCount)
	// e.srcFrame.pkt_dts = e.srcFrame.pts
	e.dstFrame.pts = C.int64_t(e.frameCount)
	e.dstFrame.pkt_dts = e.dstFrame.pts

	// Manually force keyframes every second, removing the need to rely on
	// gop_size or other encoder settings. This is necessary for the segmenter
	// to split the video files at keyframe boundaries.
	// if e.frameCount%int64(e.codecCtx.time_base.den) == 0 {
	// 	e.srcFrame.key_frame = 1
	// 	e.srcFrame.pict_type = C.AV_PICTURE_TYPE_I
	// } else {
	// 	e.srcFrame.key_frame = 0
	// 	e.srcFrame.pict_type = C.AV_PICTURE_TYPE_NONE
	// }
	if e.frameCount%int64(e.codecCtx.time_base.den) == 0 {
		e.dstFrame.key_frame = 1
		e.dstFrame.pict_type = C.AV_PICTURE_TYPE_I
	} else {
		e.dstFrame.key_frame = 0
		e.dstFrame.pict_type = C.AV_PICTURE_TYPE_NONE
	}

	ret := C.avcodec_send_frame(e.codecCtx, e.dstFrame)
	if ret < 0 {
		return nil, 0, 0, fmt.Errorf("avcodec_send_frame: %s", ffmpegError(ret))
	}
	pkt := C.av_packet_alloc()
	if pkt == nil {
		return nil, 0, 0, errors.New("could not allocate packet")
	}
	// Safe to free the packet since we copy later.
	defer C.av_packet_free(&pkt)
	ret = C.avcodec_receive_packet(e.codecCtx, pkt)
	if ret < 0 {
		return nil, 0, 0, fmt.Errorf("avcodec_receive_packet failed %s", ffmpegError(ret))
	}

	// Convert the encoded data to a Go byte slice. This is a necessary copy
	// to prevent dangling pointer in C memory. By copying to a Go bytes we can
	// allow the frame to be garbage collected automatically.
	encodedData := C.GoBytes(unsafe.Pointer(pkt.data), pkt.size)
	pts := int64(pkt.pts)
	dts := int64(pkt.dts)
	e.frameCount++
	// return encoded data

	return encodedData, pts, dts, nil
}

func (e *encoder) close() {
	C.avcodec_close(e.codecCtx)
	C.av_frame_free(&e.srcFrame)
	C.avcodec_free_context(&e.codecCtx)
}

// convert the AVFrame to YUV420p format using swscale
func (e *encoder) frameToYUV420p() error {
	swsCtx := C.sws_getContext(
		e.srcFrame.width, e.srcFrame.height, C.AV_PIX_FMT_YUYV422,
		e.srcFrame.width, e.srcFrame.height, C.AV_PIX_FMT_YUV420P,
		C.SWS_BICUBIC, nil, nil, nil,
	)
	if swsCtx == nil {
		return errors.New("could not allocate sws context")
	}
	defer C.sws_freeContext(swsCtx)

	// ret := C.av_frame_get_buffer(e.dstFrame, 32)
	ret := C.av_frame_get_buffer(e.dstFrame, 1)
	if ret < 0 {
		C.av_frame_free(&e.dstFrame)
		return fmt.Errorf("av_frame_get_buffer: %s", ffmpegError(ret))
	}

	e.dstFrame.linesize[0] = C.int(e.width)     // Y plane
	e.dstFrame.linesize[1] = C.int(e.width / 2) // U plane
	e.dstFrame.linesize[2] = C.int(e.width / 2) // V plane

	// ret = C.sws_scale(swsCtx, e.srcFrame.data, e.srcFrame.linesize, 0, e.srcFrame.height, e.srcFrame.data, e.srcFrame.linesize)
	ret = C.sws_scale(swsCtx, &e.srcFrame.data[0], &e.srcFrame.linesize[0], 0, e.srcFrame.height, &e.dstFrame.data[0], &e.dstFrame.linesize[0])
	if ret < 0 {
		C.av_frame_free(&e.dstFrame)
		return fmt.Errorf("sws_scale: %s", ffmpegError(ret))
	}

	return nil
}

// imageToYUV422 extracts unpadded YUV422 bytes from image.Image.
// This uses a row-wise copy of the Y, U, and V planes.
func imageToYUV422(img image.Image) ([]byte, error) {
	ycbcrImg, ok := img.(*image.YCbCr)
	if !ok {
		return nil, fmt.Errorf("expected type *image.YCbCr, got %s", reflect.TypeOf(img))
	}

	rect := ycbcrImg.Rect
	width := rect.Dx()
	height := rect.Dy()

	// Ensure width is even for YUV422 format
	if width%2 != 0 {
		return nil, fmt.Errorf("image width must be even for YUV422 format, got width=%d", width)
	}

	ySize := width * height
	halfWidth := width / subsampleFactor
	uSize := halfWidth * height
	vSize := uSize

	rawYUV := make([]byte, ySize+uSize+vSize)

	for y := range height {
		ySrcStart := ycbcrImg.YOffset(rect.Min.X, rect.Min.Y+y)
		cSrcStart := ycbcrImg.COffset(rect.Min.X, rect.Min.Y+y)
		yDstStart := y * width
		uDstStart := y * halfWidth
		vDstStart := y * halfWidth

		copy(rawYUV[yDstStart:yDstStart+width], ycbcrImg.Y[ySrcStart:ySrcStart+width])
		copy(rawYUV[ySize+uDstStart:ySize+uDstStart+halfWidth], ycbcrImg.Cb[cSrcStart:cSrcStart+halfWidth])
		copy(rawYUV[ySize+uSize+vDstStart:ySize+uSize+vDstStart+halfWidth], ycbcrImg.Cr[cSrcStart:cSrcStart+halfWidth])
	}

	return rawYUV, nil
}
