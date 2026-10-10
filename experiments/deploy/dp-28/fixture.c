/* DP-28 native fixture only. This is not the Rust core or the Go extension. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(int argc, char **argv) {
    char version[96] = {0};
    FILE *file = fopen("version.txt", "rb");
    if (!file || !fgets(version, sizeof(version), file)) return 2;
    if (fclose(file)) return 3;
    version[strcspn(version, "\r\n")] = '\0';
    for (size_t i = 0; version[i]; ++i) {
        if (!((version[i] >= 'a' && version[i] <= 'z') ||
              (version[i] >= '0' && version[i] <= '9') || version[i] == '-')) return 4;
    }
    file = fopen("payload.bin", "rb");
    if (!file) return 5;
    uint64_t bytes = 0, checksum = UINT64_C(14695981039346656037);
    unsigned char buffer[8192];
    size_t count;
    while ((count = fread(buffer, 1, sizeof(buffer), file)) != 0) {
        bytes += count;
        for (size_t i = 0; i < count; ++i) {
            checksum ^= buffer[i];
            checksum *= UINT64_C(1099511628211);
        }
    }
    if (ferror(file) || fclose(file)) return 6;
    printf("{\"version\":\"%s\",\"payload_bytes\":%llu,\"fnv64\":\"%016llx\"}\n",
           version, (unsigned long long)bytes, (unsigned long long)checksum);
    if (fflush(stdout)) return 7;
    /* Hold a running native process while the controller performs updates. */
    if (argc == 2 && strcmp(argv[1], "--hold") == 0) (void)getchar();
    return 0;
}
