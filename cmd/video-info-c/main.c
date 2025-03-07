#include "../../videostore/utils.h"
#include <stdio.h>

int main(int argc, char *argv[]) {
  if (argc != 2) {
    printf("usage: %s <video file>\n", argv[0]);
    return 1;
  }
  int64_t duration;
  int width;
  int height;
  char codec[VIDEO_STORE_CODEC_NAME_LEN];
  int ret = get_video_info(&duration, &width, &height, codec, argv[1]);
  if (ret == VIDEO_STORE_VIDEO_INFO_RESP_OK) {
    printf("duration: %ld\n", duration);
    printf("width: %d\n", width);
    printf("height: %d\n", height);
    printf("codec: %s\n", codec);
  } else {
    printf("error getting video info\n");
  }
  return ret;
}
