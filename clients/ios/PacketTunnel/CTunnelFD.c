#include "CTunnelFD.h"

#include <fcntl.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <sys/types.h>

/*
 * iOS 26 no longer ships sys/kern_control.h in the public SDK, while the
 * NetworkExtension utun control ABI remains unchanged. Keep the minimal ABI
 * declarations local instead of reaching for a private framework header.
 */
#define NEPROTO_CTLIOCGINFO 0xc0644e03UL

struct neproto_ctl_info {
    uint32_t ctl_id;
    char ctl_name[96];
};

struct neproto_sockaddr_ctl {
    uint8_t sc_len;
    uint8_t sc_family;
    uint16_t ss_sysaddr;
    uint32_t sc_id;
    uint32_t sc_unit;
    uint32_t sc_reserved[5];
};

static int32_t neproto_tunnel_file_descriptor(void)
{
    struct neproto_ctl_info info = { 0 };
    strlcpy(info.ctl_name, "com.apple.net.utun_control", sizeof(info.ctl_name));

    for (int32_t fd = 0; fd <= 1024; fd++) {
        struct neproto_sockaddr_ctl address = { 0 };
        socklen_t length = sizeof(address);
        if (getpeername(fd, (struct sockaddr *)&address, &length) != 0 ||
            address.sc_family != AF_SYSTEM) {
            continue;
        }
        if (info.ctl_id == 0 && ioctl(fd, NEPROTO_CTLIOCGINFO, &info) != 0) {
            continue;
        }
        if (address.sc_id == info.ctl_id) {
            return fd;
        }
    }
    return -1;
}

int32_t neproto_duplicate_tunnel_file_descriptor(void)
{
    int32_t fd = neproto_tunnel_file_descriptor();
    if (fd < 0) {
        return -1;
    }
    return fcntl(fd, F_DUPFD_CLOEXEC, 0);
}
