#include "utils.h"

// int get_video_duration(int64_t *duration,   // OUT
//                        const char *filename // IN
// ) {
//     AVFormatContext *fmt_ctx = NULL;
//     int ret;
//     if ((ret = avformat_open_input(&fmt_ctx, filename, NULL, NULL)) < 0) {
//         return ret;
//     }
//     if ((ret = avformat_find_stream_info(fmt_ctx, NULL)) < 0) {
//         avformat_close_input(&fmt_ctx);
//     return ret;
//     }
//     *duration = fmt_ctx->duration;
//     if (*duration == AV_NOPTS_VALUE) {
//         avformat_close_input(&fmt_ctx);
//         return VIDEO_STORE_DURATION_RESP_ERROR;
//     }
//     avformat_close_input(&fmt_ctx);
//     return VIDEO_STORE_DURATION_RESP_OK;
// }

#define CODEC_NAME_LEN 64

int get_video_info(int64_t *duration,   // OUT
                   int *width,          // OUT
                   int *height,         // OUT
                   char *codec,         // OUT
                   const char *filename // IN
)
{
    AVFormatContext *fmt_ctx = NULL;
    int ret;

    if ((ret = avformat_open_input(&fmt_ctx, filename, NULL, NULL)) < 0) {
        return ret;
    }
    if ((ret = avformat_find_stream_info(fmt_ctx, NULL)) < 0) {
        avformat_close_input(&fmt_ctx);
        return ret;
    }

    *duration = fmt_ctx->duration;
    if (*duration == AV_NOPTS_VALUE) {
        avformat_close_input(&fmt_ctx);
        return VIDEO_STORE_VIDEO_INFO_RESP_ERROR;
    }

    *width = 0;
    *height = 0;
    codec[0] = '\0'; // make sure it's empty

    // Choose first video stream and fetch parameters.
    for (unsigned i = 0; i < fmt_ctx->nb_streams; i++) {
        AVStream *st = fmt_ctx->streams[i];
        if (st->codecpar->codec_type == AVMEDIA_TYPE_VIDEO) {
            *width = st->codecpar->width;
            *height = st->codecpar->height;
            const char *codecName = avcodec_get_name(st->codecpar->codec_id);
            if (codecName) {
                strncpy(codec, codecName, CODEC_NAME_LEN - 1);
                codec[CODEC_NAME_LEN - 1] = '\0'; // ensure null-termination
            }
            break;
        }
    }

    if (*width == 0 || *height == 0) {
        avformat_close_input(&fmt_ctx);
        return VIDEO_STORE_VIDEO_INFO_RESP_ERROR;
    }

    if (codec[0] == '\0') {
        avformat_close_input(&fmt_ctx);
        return VIDEO_STORE_VIDEO_INFO_RESP_ERROR;
    }

    avformat_close_input(&fmt_ctx);
    return VIDEO_STORE_VIDEO_INFO_RESP_OK;
}
