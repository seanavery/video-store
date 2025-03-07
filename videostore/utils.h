#ifndef VIAM_VIDEOSTORE_UTILS_H
#define VIAM_VIDEOSTORE_UTILS_H
#include <libavformat/avformat.h>
int get_video_info(int64_t *duration, int *width, int *height, char *codec, const char *filename);
#define VIDEO_STORE_VIDEO_INFO_RESP_ERROR 1
#define VIDEO_STORE_VIDEO_INFO_RESP_OK 0
#define VIDEO_STORE_CODEC_NAME_LEN 64
#endif /* VIAM_VIDEOSTORE_UTILS_H */
