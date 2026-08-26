#ifndef NEPROTO_C_TUNNEL_FD_H
#define NEPROTO_C_TUNNEL_FD_H

#include <stdint.h>

int32_t neproto_duplicate_tunnel_file_descriptor(void);
void neproto_close_tunnel_file_descriptor(int32_t file_descriptor);

#endif
