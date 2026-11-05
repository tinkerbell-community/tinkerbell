/* EFI builds get CONSOLE_EFI by default; defining it unconditionally would
 * pull the EFI console into BIOS images (undionly.kpxe), which then fail to
 * link against efi_systab. */
#if defined ( PLATFORM_efi )
#define CONSOLE_EFI
#endif
#define CONSOLE_SERIAL
#define	CONSOLE_SYSLOG
