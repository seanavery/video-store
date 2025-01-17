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

	"go.viam.com/rdk/logging"
)

type mimeHandler struct {
	logger       logging.Logger
	yuyvSrcFrame *C.AVFrame
	yuyvDstFrame *C.AVFrame
	yuyvSwCtx    *C.struct_SwsContext
	jpegDstFrame *C.AVFrame
	jpegCodecCtx *C.AVCodecContext
}

func newMimeHandler(logger logging.Logger) *mimeHandler {
	return &mimeHandler{
		logger: logger,
	}
}

func (mh *mimeHandler) yuyvToYUV420p(frameBytes []byte, width, height int) (*C.AVFrame, error) {
	if mh.yuyvSwCtx == nil || width != int(mh.yuyvDstFrame.width) || height != int(mh.yuyvDstFrame.height) {
		if err := mh.initYUYVCtx(width, height); err != nil {
			return nil, err
		}
	}

	// Fill src frame with YUYV data bytes.
	// We use C.CBytes to allocate memory in C heap and defer free it.
	yuyvBytes := C.CBytes(frameBytes)
	defer C.free(yuyvBytes)
	mh.yuyvSrcFrame.data[0] = (*C.uint8_t)(yuyvBytes)
	mh.yuyvSrcFrame.linesize[0] = C.int(width * subsampleFactor)

	// Convert YUYV to YUV420p.
	ret := C.sws_scale(
		mh.yuyvSwCtx,
		&mh.yuyvSrcFrame.data[0],
		&mh.yuyvSrcFrame.linesize[0],
		0,
		C.int(height),
		&mh.yuyvDstFrame.data[0],
		&mh.yuyvDstFrame.linesize[0],
	)
	if ret < 0 {
		return nil, errors.New("failed to convert YUYV to YUV420p")
	}

	return mh.yuyvDstFrame, nil
}

func (mh *mimeHandler) initYUYVCtx(width, height int) error {
	mh.logger.Infof("Initializing YUYV sws context with width %d and height %d", width, height)
	if mh.yuyvSwCtx != nil {
		C.sws_freeContext(mh.yuyvSwCtx)
	}
	if mh.yuyvDstFrame != nil {
		C.av_frame_free(&mh.yuyvDstFrame)
	}
	if mh.yuyvSrcFrame != nil {
		C.av_frame_free(&mh.yuyvSrcFrame)
	}

	mh.yuyvSwCtx = C.sws_getContext(C.int(width), C.int(height), C.AV_PIX_FMT_YUYV422,
		C.int(width), C.int(height), C.AV_PIX_FMT_YUV420P,
		C.SWS_FAST_BILINEAR, nil, nil, nil)
	if mh.yuyvSwCtx == nil {
		return errors.New("failed to create YUYV to YUV420p conversion context")
	}

	mh.yuyvSrcFrame = C.av_frame_alloc()
	if mh.yuyvSrcFrame == nil {
		return errors.New("failed to allocate YUYV source frame")
	}
	mh.yuyvSrcFrame.width = C.int(width)
	mh.yuyvSrcFrame.height = C.int(height)
	mh.yuyvSrcFrame.format = C.AV_PIX_FMT_YUYV422
	// allocate buffer for YUYV data
	ret := C.av_frame_get_buffer(mh.yuyvSrcFrame, 32)
	if ret < 0 {
		return errors.New("failed to allocate buffer for YUYV source frame")
	}

	mh.yuyvDstFrame = C.av_frame_alloc()
	if mh.yuyvDstFrame == nil {
		return errors.New("failed to allocate YUV420p destination frame")
	}
	mh.yuyvDstFrame.width = C.int(width)
	mh.yuyvDstFrame.height = C.int(height)
	mh.yuyvDstFrame.format = C.AV_PIX_FMT_YUV420P
	ret = C.av_frame_get_buffer(mh.yuyvDstFrame, 32)
	if ret < 0 {
		return errors.New("failed to allocate buffer for YUV420p destination frame")
	}

	return nil
}

func (mh *mimeHandler) decodeJPEG(frameBytes []byte) (*C.AVFrame, error) {
	// fill a jpeg pkt with the frame bytes
	dataPtr := C.CBytes(frameBytes)
	defer C.free(dataPtr)
	pkt := C.AVPacket{
		data: (*C.uint8_t)(dataPtr),
		size: C.int(len(frameBytes)),
	}
	// defer free the pkt data
	if mh.jpegCodecCtx == nil {
		if err := mh.initJPEGDecoder(); err != nil {
			return nil, err
		}
	}
	if mh.jpegCodecCtx == nil {
		return nil, errors.New("JPEG decoder not initialized")
	}
	// Allocate the destination frame if not already allocated
	if mh.jpegDstFrame == nil {
		mh.jpegDstFrame = C.av_frame_alloc()
		if mh.jpegDstFrame == nil {
			return nil, errors.New("could not allocate destination frame")
		}
	}
	// The mjpeg decoder can figure out width and height from the frame bytes.
	// We don't need to pass width and height to initJPEGDecoder and it can
	// recover from a change in resolution.
	ret := C.avcodec_send_packet(mh.jpegCodecCtx, &pkt)
	if ret < 0 {
		return nil, errors.New("failed to send packet to JPEG decoder")
	}
	// Receive frame will allocate the frame buffer so we do not need to
	// manually call av_frame_get_buffer.
	ret = C.avcodec_receive_frame(mh.jpegCodecCtx, mh.jpegDstFrame)
	if ret < 0 {
		return nil, errors.New("failed to receive frame from JPEG decoder")
	}

	return mh.jpegDstFrame, nil
}

func (mh *mimeHandler) initJPEGDecoder() error {
	mh.logger.Infof("Initializing JPEG decoder")
	if mh.jpegCodecCtx != nil {
		C.avcodec_free_context(&mh.jpegCodecCtx)
	}
	if mh.jpegDstFrame != nil {
		C.av_frame_free(&mh.jpegDstFrame)
	}

	// initialize codec context
	codec := C.avcodec_find_decoder(C.AV_CODEC_ID_MJPEG)
	if codec == nil {
		return errors.New("failed to find JPEG codec")
	}
	mh.jpegCodecCtx = C.avcodec_alloc_context3(codec)
	if mh.jpegCodecCtx == nil {
		return errors.New("failed to allocate JPEG codec context")
	}
	mh.jpegCodecCtx.pix_fmt = C.AV_PIX_FMT_YUV420P
	ret := C.avcodec_open2(mh.jpegCodecCtx, codec, nil)
	if ret < 0 {
		return errors.New("failed to open JPEG codec")
	}

	return nil
}

/*
	// initialize the destination frame
	mh.jpegDstFrame = C.av_frame_alloc()
	if mh.jpegDstFrame == nil {
		return errors.New("failed to allocate JPEG destination frame")
	}
	mh.jpegDstFrame.width = C.int(width)
	mh.jpegDstFrame.height = C.int(height)
	mh.jpegDstFrame.format = C.AV_PIX_FMT_YUV420P
	ret = C.av_frame_get_buffer(mh.jpegDstFrame, 32)
	if ret < 0 {
		return errors.New("failed to allocate buffer for JPEG destination frame")
	}
*/

func (mh *mimeHandler) close() {
	if mh.yuyvSwCtx != nil {
		C.sws_freeContext(mh.yuyvSwCtx)
	}
	if mh.yuyvDstFrame != nil {
		C.av_frame_free(&mh.yuyvDstFrame)
	}
	if mh.yuyvSrcFrame != nil {
		C.av_frame_free(&mh.yuyvSrcFrame)
	}
}