package edk2

import (
	_ "embed"
	"fmt"
	"net"

	"github.com/tinkerbell/tinkerbell/smee/internal/firmware/edk2/efi"
	"github.com/tinkerbell/tinkerbell/smee/internal/firmware/edk2/varstore"
)

const FirmwareFileName = "RPI_EFI.fd"

// RpiEfi returns the RPI_EFI.fd file.
//
//go:embed RPI_EFI.fd
var RpiEfi []byte

// FixupDat returns the fixup.dat file.
//
//go:embed fixup4.dat
var Fixup4Dat []byte

// Start4ElfDat returns the start4.elf file.
//
//go:embed start4.elf
var Start4ElfDat []byte

// Bcm2711Rpi4BDtb returns the bcm2711-rpi-4-b.dtb file.
//
//go:embed broadcom/bcm2711-rpi-4-b.dtb
var Bcm2711Rpi4BDtb []byte

// Bcm2711Rpi400Dtb returns the bcm2711-rpi-400.dtb file.
//
//go:embed broadcom/bcm2711-rpi-400.dtb
var Bcm2711Rpi400Dtb []byte

// Bcm2711RpiCm4Dtb returns the bcm2711-rpi-cm4.dtb file.
//
//go:embed broadcom/bcm2711-rpi-cm4.dtb
var Bcm2711RpiCm4Dtb []byte

// OverlaysMiniUartBtDtbo returns the overlays/miniuart-bt.dtbo file.
//
//go:embed overlays/miniuart-bt.dtbo
var OverlaysMiniUartBtDtbo []byte

// OverlaysUpstreamPi4Dtbo returns the overlays/upstream-pi4.dtbo file.
//
//go:embed overlays/upstream-pi4.dtbo
var OverlaysUpstreamPi4Dtbo []byte

// OverlaysRpiPoePlusDtbo returns the overlays/rpi-poe-plus.dtbo file.
//
//go:embed overlays/rpi-poe-plus.dtbo
var OverlaysRpiPoePlusDtbo []byte

// OverlaysActLedDtbo returns the overlays/act-led.dtbo file.
//
//go:embed overlays/act-led.dtbo
var OverlaysActLedDtbo []byte

// OverlaysAdafruitSt7735rDtbo returns the overlays/adafruit-st7735r.dtbo file.
//
//go:embed overlays/adafruit-st7735r.dtbo
var OverlaysAdafruitSt7735rDtbo []byte

// OverlaysAdafruit18Dtbo returns the overlays/adafruit18.dtbo file.
//
//go:embed overlays/adafruit18.dtbo
var OverlaysAdafruit18Dtbo []byte

// OverlaysAdau1977AdcDtbo returns the overlays/adau1977-adc.dtbo file.
//
//go:embed overlays/adau1977-adc.dtbo
var OverlaysAdau1977AdcDtbo []byte

// OverlaysAdau7002SimpleDtbo returns the overlays/adau7002-simple.dtbo file.
//
//go:embed overlays/adau7002-simple.dtbo
var OverlaysAdau7002SimpleDtbo []byte

// OverlaysAds1015Dtbo returns the overlays/ads1015.dtbo file.
//
//go:embed overlays/ads1015.dtbo
var OverlaysAds1015Dtbo []byte

// OverlaysAds1115Dtbo returns the overlays/ads1115.dtbo file.
//
//go:embed overlays/ads1115.dtbo
var OverlaysAds1115Dtbo []byte

// OverlaysAds7846Dtbo returns the overlays/ads7846.dtbo file.
//
//go:embed overlays/ads7846.dtbo
var OverlaysAds7846Dtbo []byte

// OverlaysAdv7282mDtbo returns the overlays/adv7282m.dtbo file.
//
//go:embed overlays/adv7282m.dtbo
var OverlaysAdv7282mDtbo []byte

// OverlaysAdv728xMDtbo returns the overlays/adv728x-m.dtbo file.
//
//go:embed overlays/adv728x-m.dtbo
var OverlaysAdv728xMDtbo []byte

// OverlaysAkkordionIqdacplusDtbo returns the overlays/akkordion-iqdacplus.dtbo file.
//
//go:embed overlays/akkordion-iqdacplus.dtbo
var OverlaysAkkordionIqdacplusDtbo []byte

// OverlaysAlloBossDacPcm512xAudioDtbo returns the overlays/allo-boss-dac-pcm512x-audio.dtbo file.
//
//go:embed overlays/allo-boss-dac-pcm512x-audio.dtbo
var OverlaysAlloBossDacPcm512xAudioDtbo []byte

// OverlaysAlloBoss2DacAudioDtbo returns the overlays/allo-boss2-dac-audio.dtbo file.
//
//go:embed overlays/allo-boss2-dac-audio.dtbo
var OverlaysAlloBoss2DacAudioDtbo []byte

// OverlaysAlloDigioneDtbo returns the overlays/allo-digione.dtbo file.
//
//go:embed overlays/allo-digione.dtbo
var OverlaysAlloDigioneDtbo []byte

// OverlaysAlloKatanaDacAudioDtbo returns the overlays/allo-katana-dac-audio.dtbo file.
//
//go:embed overlays/allo-katana-dac-audio.dtbo
var OverlaysAlloKatanaDacAudioDtbo []byte

// OverlaysAlloPianoDacPcm512xAudioDtbo returns the overlays/allo-piano-dac-pcm512x-audio.dtbo file.
//
//go:embed overlays/allo-piano-dac-pcm512x-audio.dtbo
var OverlaysAlloPianoDacPcm512xAudioDtbo []byte

// OverlaysAlloPianoDacPlusPcm512xAudioDtbo returns the overlays/allo-piano-dac-plus-pcm512x-audio.dtbo file.
//
//go:embed overlays/allo-piano-dac-plus-pcm512x-audio.dtbo
var OverlaysAlloPianoDacPlusPcm512xAudioDtbo []byte

// OverlaysAnyspiDtbo returns the overlays/anyspi.dtbo file.
//
//go:embed overlays/anyspi.dtbo
var OverlaysAnyspiDtbo []byte

// OverlaysApds9960Dtbo returns the overlays/apds9960.dtbo file.
//
//go:embed overlays/apds9960.dtbo
var OverlaysApds9960Dtbo []byte

// OverlaysApplepiDacDtbo returns the overlays/applepi-dac.dtbo file.
//
//go:embed overlays/applepi-dac.dtbo
var OverlaysApplepiDacDtbo []byte

// OverlaysArducam64mpDtbo returns the overlays/arducam-64mp.dtbo file.
//
//go:embed overlays/arducam-64mp.dtbo
var OverlaysArducam64mpDtbo []byte

// OverlaysArducamPivarietyDtbo returns the overlays/arducam-pivariety.dtbo file.
//
//go:embed overlays/arducam-pivariety.dtbo
var OverlaysArducamPivarietyDtbo []byte

// OverlaysAt86rf233Dtbo returns the overlays/at86rf233.dtbo file.
//
//go:embed overlays/at86rf233.dtbo
var OverlaysAt86rf233Dtbo []byte

// OverlaysAudioinjectorAddonsDtbo returns the overlays/audioinjector-addons.dtbo file.
//
//go:embed overlays/audioinjector-addons.dtbo
var OverlaysAudioinjectorAddonsDtbo []byte

// OverlaysAudioinjectorBareI2sDtbo returns the overlays/audioinjector-bare-i2s.dtbo file.
//
//go:embed overlays/audioinjector-bare-i2s.dtbo
var OverlaysAudioinjectorBareI2sDtbo []byte

// OverlaysAudioinjectorIsolatedSoundcardDtbo returns the overlays/audioinjector-isolated-soundcard.dtbo file.
//
//go:embed overlays/audioinjector-isolated-soundcard.dtbo
var OverlaysAudioinjectorIsolatedSoundcardDtbo []byte

// OverlaysAudioinjectorUltraDtbo returns the overlays/audioinjector-ultra.dtbo file.
//
//go:embed overlays/audioinjector-ultra.dtbo
var OverlaysAudioinjectorUltraDtbo []byte

// OverlaysAudioinjectorWm8731AudioDtbo returns the overlays/audioinjector-wm8731-audio.dtbo file.
//
//go:embed overlays/audioinjector-wm8731-audio.dtbo
var OverlaysAudioinjectorWm8731AudioDtbo []byte

// OverlaysAudiosensePiDtbo returns the overlays/audiosense-pi.dtbo file.
//
//go:embed overlays/audiosense-pi.dtbo
var OverlaysAudiosensePiDtbo []byte

// OverlaysAudremapPi5Dtbo returns the overlays/audremap-pi5.dtbo file.
//
//go:embed overlays/audremap-pi5.dtbo
var OverlaysAudremapPi5Dtbo []byte

// OverlaysAudremapDtbo returns the overlays/audremap.dtbo file.
//
//go:embed overlays/audremap.dtbo
var OverlaysAudremapDtbo []byte

// OverlaysBalenaFinDtbo returns the overlays/balena-fin.dtbo file.
//
//go:embed overlays/balena-fin.dtbo
var OverlaysBalenaFinDtbo []byte

// OverlaysBcm2712d0Dtbo returns the overlays/bcm2712d0.dtbo file.
//
//go:embed overlays/bcm2712d0.dtbo
var OverlaysBcm2712d0Dtbo []byte

// OverlaysCameraMux2portDtbo returns the overlays/camera-mux-2port.dtbo file.
//
//go:embed overlays/camera-mux-2port.dtbo
var OverlaysCameraMux2portDtbo []byte

// OverlaysCameraMux4portDtbo returns the overlays/camera-mux-4port.dtbo file.
//
//go:embed overlays/camera-mux-4port.dtbo
var OverlaysCameraMux4portDtbo []byte

// OverlaysCap1106Dtbo returns the overlays/cap1106.dtbo file.
//
//go:embed overlays/cap1106.dtbo
var OverlaysCap1106Dtbo []byte

// OverlaysChipcap2Dtbo returns the overlays/chipcap2.dtbo file.
//
//go:embed overlays/chipcap2.dtbo
var OverlaysChipcap2Dtbo []byte

// OverlaysChipdipDacDtbo returns the overlays/chipdip-dac.dtbo file.
//
//go:embed overlays/chipdip-dac.dtbo
var OverlaysChipdipDacDtbo []byte

// OverlaysCirrusWm5102Dtbo returns the overlays/cirrus-wm5102.dtbo file.
//
//go:embed overlays/cirrus-wm5102.dtbo
var OverlaysCirrusWm5102Dtbo []byte

// OverlaysCmSwapI2c0Dtbo returns the overlays/cm-swap-i2c0.dtbo file.
//
//go:embed overlays/cm-swap-i2c0.dtbo
var OverlaysCmSwapI2c0Dtbo []byte

// OverlaysCmaDtbo returns the overlays/cma.dtbo file.
//
//go:embed overlays/cma.dtbo
var OverlaysCmaDtbo []byte

// OverlaysCrystalfontzCfa050PiMDtbo returns the overlays/crystalfontz-cfa050_pi_m.dtbo file.
//
//go:embed overlays/crystalfontz-cfa050_pi_m.dtbo
var OverlaysCrystalfontzCfa050PiMDtbo []byte

// OverlaysCutiepiPanelDtbo returns the overlays/cutiepi-panel.dtbo file.
//
//go:embed overlays/cutiepi-panel.dtbo
var OverlaysCutiepiPanelDtbo []byte

// OverlaysDacberry400Dtbo returns the overlays/dacberry400.dtbo file.
//
//go:embed overlays/dacberry400.dtbo
var OverlaysDacberry400Dtbo []byte

// OverlaysDht11Dtbo returns the overlays/dht11.dtbo file.
//
//go:embed overlays/dht11.dtbo
var OverlaysDht11Dtbo []byte

// OverlaysDionaudioKiwiDtbo returns the overlays/dionaudio-kiwi.dtbo file.
//
//go:embed overlays/dionaudio-kiwi.dtbo
var OverlaysDionaudioKiwiDtbo []byte

// OverlaysDionaudioLocoV2Dtbo returns the overlays/dionaudio-loco-v2.dtbo file.
//
//go:embed overlays/dionaudio-loco-v2.dtbo
var OverlaysDionaudioLocoV2Dtbo []byte

// OverlaysDionaudioLocoDtbo returns the overlays/dionaudio-loco.dtbo file.
//
//go:embed overlays/dionaudio-loco.dtbo
var OverlaysDionaudioLocoDtbo []byte

// OverlaysDisableBtPi5Dtbo returns the overlays/disable-bt-pi5.dtbo file.
//
//go:embed overlays/disable-bt-pi5.dtbo
var OverlaysDisableBtPi5Dtbo []byte

// OverlaysDisableBtDtbo returns the overlays/disable-bt.dtbo file.
//
//go:embed overlays/disable-bt.dtbo
var OverlaysDisableBtDtbo []byte

// OverlaysDisableEmmc2Dtbo returns the overlays/disable-emmc2.dtbo file.
//
//go:embed overlays/disable-emmc2.dtbo
var OverlaysDisableEmmc2Dtbo []byte

// OverlaysDisableWifiPi5Dtbo returns the overlays/disable-wifi-pi5.dtbo file.
//
//go:embed overlays/disable-wifi-pi5.dtbo
var OverlaysDisableWifiPi5Dtbo []byte

// OverlaysDisableWifiDtbo returns the overlays/disable-wifi.dtbo file.
//
//go:embed overlays/disable-wifi.dtbo
var OverlaysDisableWifiDtbo []byte

// OverlaysDpi18Dtbo returns the overlays/dpi18.dtbo file.
//
//go:embed overlays/dpi18.dtbo
var OverlaysDpi18Dtbo []byte

// OverlaysDpi18cpadhiDtbo returns the overlays/dpi18cpadhi.dtbo file.
//
//go:embed overlays/dpi18cpadhi.dtbo
var OverlaysDpi18cpadhiDtbo []byte

// OverlaysDpi24Dtbo returns the overlays/dpi24.dtbo file.
//
//go:embed overlays/dpi24.dtbo
var OverlaysDpi24Dtbo []byte

// OverlaysDrawsDtbo returns the overlays/draws.dtbo file.
//
//go:embed overlays/draws.dtbo
var OverlaysDrawsDtbo []byte

// OverlaysDwcOtgDeprecatedDtbo returns the overlays/dwc-otg-deprecated.dtbo file.
//
//go:embed overlays/dwc-otg-deprecated.dtbo
var OverlaysDwcOtgDeprecatedDtbo []byte

// OverlaysDwc2Dtbo returns the overlays/dwc2.dtbo file.
//
//go:embed overlays/dwc2.dtbo
var OverlaysDwc2Dtbo []byte

// OverlaysEdtFt5406Dtbo returns the overlays/edt-ft5406.dtbo file.
//
//go:embed overlays/edt-ft5406.dtbo
var OverlaysEdtFt5406Dtbo []byte

// OverlaysEnc28j60Spi2Dtbo returns the overlays/enc28j60-spi2.dtbo file.
//
//go:embed overlays/enc28j60-spi2.dtbo
var OverlaysEnc28j60Spi2Dtbo []byte

// OverlaysEnc28j60Dtbo returns the overlays/enc28j60.dtbo file.
//
//go:embed overlays/enc28j60.dtbo
var OverlaysEnc28j60Dtbo []byte

// OverlaysExc3000Dtbo returns the overlays/exc3000.dtbo file.
//
//go:embed overlays/exc3000.dtbo
var OverlaysExc3000Dtbo []byte

// OverlaysEzsound6x8isoDtbo returns the overlays/ezsound-6x8iso.dtbo file.
//
//go:embed overlays/ezsound-6x8iso.dtbo
var OverlaysEzsound6x8isoDtbo []byte

// OverlaysFbtftDtbo returns the overlays/fbtft.dtbo file.
//
//go:embed overlays/fbtft.dtbo
var OverlaysFbtftDtbo []byte

// OverlaysFePiAudioDtbo returns the overlays/fe-pi-audio.dtbo file.
//
//go:embed overlays/fe-pi-audio.dtbo
var OverlaysFePiAudioDtbo []byte

// OverlaysFsmDemoDtbo returns the overlays/fsm-demo.dtbo file.
//
//go:embed overlays/fsm-demo.dtbo
var OverlaysFsmDemoDtbo []byte

// OverlaysGc9a01Dtbo returns the overlays/gc9a01.dtbo file.
//
//go:embed overlays/gc9a01.dtbo
var OverlaysGc9a01Dtbo []byte

// OverlaysGhostAmpDtbo returns the overlays/ghost-amp.dtbo file.
//
//go:embed overlays/ghost-amp.dtbo
var OverlaysGhostAmpDtbo []byte

// OverlaysGoodixDtbo returns the overlays/goodix.dtbo file.
//
//go:embed overlays/goodix.dtbo
var OverlaysGoodixDtbo []byte

// OverlaysGooglevoicehatSoundcardDtbo returns the overlays/googlevoicehat-soundcard.dtbo file.
//
//go:embed overlays/googlevoicehat-soundcard.dtbo
var OverlaysGooglevoicehatSoundcardDtbo []byte

// OverlaysGpioChargerDtbo returns the overlays/gpio-charger.dtbo file.
//
//go:embed overlays/gpio-charger.dtbo
var OverlaysGpioChargerDtbo []byte

// OverlaysGpioFanDtbo returns the overlays/gpio-fan.dtbo file.
//
//go:embed overlays/gpio-fan.dtbo
var OverlaysGpioFanDtbo []byte

// OverlaysGpioHogDtbo returns the overlays/gpio-hog.dtbo file.
//
//go:embed overlays/gpio-hog.dtbo
var OverlaysGpioHogDtbo []byte

// OverlaysGpioIrTxDtbo returns the overlays/gpio-ir-tx.dtbo file.
//
//go:embed overlays/gpio-ir-tx.dtbo
var OverlaysGpioIrTxDtbo []byte

// OverlaysGpioIrDtbo returns the overlays/gpio-ir.dtbo file.
//
//go:embed overlays/gpio-ir.dtbo
var OverlaysGpioIrDtbo []byte

// OverlaysGpioKeyDtbo returns the overlays/gpio-key.dtbo file.
//
//go:embed overlays/gpio-key.dtbo
var OverlaysGpioKeyDtbo []byte

// OverlaysGpioLedDtbo returns the overlays/gpio-led.dtbo file.
//
//go:embed overlays/gpio-led.dtbo
var OverlaysGpioLedDtbo []byte

// OverlaysGpioNoBank0IrqDtbo returns the overlays/gpio-no-bank0-irq.dtbo file.
//
//go:embed overlays/gpio-no-bank0-irq.dtbo
var OverlaysGpioNoBank0IrqDtbo []byte

// OverlaysGpioNoIrqDtbo returns the overlays/gpio-no-irq.dtbo file.
//
//go:embed overlays/gpio-no-irq.dtbo
var OverlaysGpioNoIrqDtbo []byte

// OverlaysGpioPoweroffDtbo returns the overlays/gpio-poweroff.dtbo file.
//
//go:embed overlays/gpio-poweroff.dtbo
var OverlaysGpioPoweroffDtbo []byte

// OverlaysGpioShutdownDtbo returns the overlays/gpio-shutdown.dtbo file.
//
//go:embed overlays/gpio-shutdown.dtbo
var OverlaysGpioShutdownDtbo []byte

// OverlaysHatMapDtb returns the overlays/hat_map.dtb file.
//
//go:embed overlays/hat_map.dtb
var OverlaysHatMapDtb []byte

// OverlaysHd44780I2cLcdDtbo returns the overlays/hd44780-i2c-lcd.dtbo file.
//
//go:embed overlays/hd44780-i2c-lcd.dtbo
var OverlaysHd44780I2cLcdDtbo []byte

// OverlaysHd44780LcdDtbo returns the overlays/hd44780-lcd.dtbo file.
//
//go:embed overlays/hd44780-lcd.dtbo
var OverlaysHd44780LcdDtbo []byte

// OverlaysHdmiBacklightHwhackGpioDtbo returns the overlays/hdmi-backlight-hwhack-gpio.dtbo file.
//
//go:embed overlays/hdmi-backlight-hwhack-gpio.dtbo
var OverlaysHdmiBacklightHwhackGpioDtbo []byte

// OverlaysHifiberryAdcDtbo returns the overlays/hifiberry-adc.dtbo file.
//
//go:embed overlays/hifiberry-adc.dtbo
var OverlaysHifiberryAdcDtbo []byte

// OverlaysHifiberryAdc8xDtbo returns the overlays/hifiberry-adc8x.dtbo file.
//
//go:embed overlays/hifiberry-adc8x.dtbo
var OverlaysHifiberryAdc8xDtbo []byte

// OverlaysHifiberryAmpDtbo returns the overlays/hifiberry-amp.dtbo file.
//
//go:embed overlays/hifiberry-amp.dtbo
var OverlaysHifiberryAmpDtbo []byte

// OverlaysHifiberryAmp100Dtbo returns the overlays/hifiberry-amp100.dtbo file.
//
//go:embed overlays/hifiberry-amp100.dtbo
var OverlaysHifiberryAmp100Dtbo []byte

// OverlaysHifiberryAmp3Dtbo returns the overlays/hifiberry-amp3.dtbo file.
//
//go:embed overlays/hifiberry-amp3.dtbo
var OverlaysHifiberryAmp3Dtbo []byte

// OverlaysHifiberryAmp4proDtbo returns the overlays/hifiberry-amp4pro.dtbo file.
//
//go:embed overlays/hifiberry-amp4pro.dtbo
var OverlaysHifiberryAmp4proDtbo []byte

// OverlaysHifiberryDacDtbo returns the overlays/hifiberry-dac.dtbo file.
//
//go:embed overlays/hifiberry-dac.dtbo
var OverlaysHifiberryDacDtbo []byte

// OverlaysHifiberryDac8xDtbo returns the overlays/hifiberry-dac8x.dtbo file.
//
//go:embed overlays/hifiberry-dac8x.dtbo
var OverlaysHifiberryDac8xDtbo []byte

// OverlaysHifiberryDacplusProDtbo returns the overlays/hifiberry-dacplus-pro.dtbo file.
//
//go:embed overlays/hifiberry-dacplus-pro.dtbo
var OverlaysHifiberryDacplusProDtbo []byte

// OverlaysHifiberryDacplusStdDtbo returns the overlays/hifiberry-dacplus-std.dtbo file.
//
//go:embed overlays/hifiberry-dacplus-std.dtbo
var OverlaysHifiberryDacplusStdDtbo []byte

// OverlaysHifiberryDacplusDtbo returns the overlays/hifiberry-dacplus.dtbo file.
//
//go:embed overlays/hifiberry-dacplus.dtbo
var OverlaysHifiberryDacplusDtbo []byte

// OverlaysHifiberryDacplusadcDtbo returns the overlays/hifiberry-dacplusadc.dtbo file.
//
//go:embed overlays/hifiberry-dacplusadc.dtbo
var OverlaysHifiberryDacplusadcDtbo []byte

// OverlaysHifiberryDacplusadcproDtbo returns the overlays/hifiberry-dacplusadcpro.dtbo file.
//
//go:embed overlays/hifiberry-dacplusadcpro.dtbo
var OverlaysHifiberryDacplusadcproDtbo []byte

// OverlaysHifiberryDacplusdspDtbo returns the overlays/hifiberry-dacplusdsp.dtbo file.
//
//go:embed overlays/hifiberry-dacplusdsp.dtbo
var OverlaysHifiberryDacplusdspDtbo []byte

// OverlaysHifiberryDacplushdDtbo returns the overlays/hifiberry-dacplushd.dtbo file.
//
//go:embed overlays/hifiberry-dacplushd.dtbo
var OverlaysHifiberryDacplushdDtbo []byte

// OverlaysHifiberryDigiProDtbo returns the overlays/hifiberry-digi-pro.dtbo file.
//
//go:embed overlays/hifiberry-digi-pro.dtbo
var OverlaysHifiberryDigiProDtbo []byte

// OverlaysHifiberryDigiDtbo returns the overlays/hifiberry-digi.dtbo file.
//
//go:embed overlays/hifiberry-digi.dtbo
var OverlaysHifiberryDigiDtbo []byte

// OverlaysHighperiDtbo returns the overlays/highperi.dtbo file.
//
//go:embed overlays/highperi.dtbo
var OverlaysHighperiDtbo []byte

// OverlaysHy28aDtbo returns the overlays/hy28a.dtbo file.
//
//go:embed overlays/hy28a.dtbo
var OverlaysHy28aDtbo []byte

// OverlaysHy28b2017Dtbo returns the overlays/hy28b-2017.dtbo file.
//
//go:embed overlays/hy28b-2017.dtbo
var OverlaysHy28b2017Dtbo []byte

// OverlaysHy28bDtbo returns the overlays/hy28b.dtbo file.
//
//go:embed overlays/hy28b.dtbo
var OverlaysHy28bDtbo []byte

// OverlaysISabreQ2mDtbo returns the overlays/i-sabre-q2m.dtbo file.
//
//go:embed overlays/i-sabre-q2m.dtbo
var OverlaysISabreQ2mDtbo []byte

// OverlaysI2cBcm2708Dtbo returns the overlays/i2c-bcm2708.dtbo file.
//
//go:embed overlays/i2c-bcm2708.dtbo
var OverlaysI2cBcm2708Dtbo []byte

// OverlaysI2cFanDtbo returns the overlays/i2c-fan.dtbo file.
//
//go:embed overlays/i2c-fan.dtbo
var OverlaysI2cFanDtbo []byte

// OverlaysI2cGpioDtbo returns the overlays/i2c-gpio.dtbo file.
//
//go:embed overlays/i2c-gpio.dtbo
var OverlaysI2cGpioDtbo []byte

// OverlaysI2cMuxDtbo returns the overlays/i2c-mux.dtbo file.
//
//go:embed overlays/i2c-mux.dtbo
var OverlaysI2cMuxDtbo []byte

// OverlaysI2cPwmPca9685aDtbo returns the overlays/i2c-pwm-pca9685a.dtbo file.
//
//go:embed overlays/i2c-pwm-pca9685a.dtbo
var OverlaysI2cPwmPca9685aDtbo []byte

// OverlaysI2cRtcGpioDtbo returns the overlays/i2c-rtc-gpio.dtbo file.
//
//go:embed overlays/i2c-rtc-gpio.dtbo
var OverlaysI2cRtcGpioDtbo []byte

// OverlaysI2cRtcDtbo returns the overlays/i2c-rtc.dtbo file.
//
//go:embed overlays/i2c-rtc.dtbo
var OverlaysI2cRtcDtbo []byte

// OverlaysI2cSensorDtbo returns the overlays/i2c-sensor.dtbo file.
//
//go:embed overlays/i2c-sensor.dtbo
var OverlaysI2cSensorDtbo []byte

// OverlaysI2c0Pi5Dtbo returns the overlays/i2c0-pi5.dtbo file.
//
//go:embed overlays/i2c0-pi5.dtbo
var OverlaysI2c0Pi5Dtbo []byte

// OverlaysI2c0Dtbo returns the overlays/i2c0.dtbo file.
//
//go:embed overlays/i2c0.dtbo
var OverlaysI2c0Dtbo []byte

// OverlaysI2c1Pi5Dtbo returns the overlays/i2c1-pi5.dtbo file.
//
//go:embed overlays/i2c1-pi5.dtbo
var OverlaysI2c1Pi5Dtbo []byte

// OverlaysI2c1Dtbo returns the overlays/i2c1.dtbo file.
//
//go:embed overlays/i2c1.dtbo
var OverlaysI2c1Dtbo []byte

// OverlaysI2c2Pi5Dtbo returns the overlays/i2c2-pi5.dtbo file.
//
//go:embed overlays/i2c2-pi5.dtbo
var OverlaysI2c2Pi5Dtbo []byte

// OverlaysI2c3Pi5Dtbo returns the overlays/i2c3-pi5.dtbo file.
//
//go:embed overlays/i2c3-pi5.dtbo
var OverlaysI2c3Pi5Dtbo []byte

// OverlaysI2c3Dtbo returns the overlays/i2c3.dtbo file.
//
//go:embed overlays/i2c3.dtbo
var OverlaysI2c3Dtbo []byte

// OverlaysI2c4Dtbo returns the overlays/i2c4.dtbo file.
//
//go:embed overlays/i2c4.dtbo
var OverlaysI2c4Dtbo []byte

// OverlaysI2c5Dtbo returns the overlays/i2c5.dtbo file.
//
//go:embed overlays/i2c5.dtbo
var OverlaysI2c5Dtbo []byte

// OverlaysI2c6Dtbo returns the overlays/i2c6.dtbo file.
//
//go:embed overlays/i2c6.dtbo
var OverlaysI2c6Dtbo []byte

// OverlaysI2sDacDtbo returns the overlays/i2s-dac.dtbo file.
//
//go:embed overlays/i2s-dac.dtbo
var OverlaysI2sDacDtbo []byte

// OverlaysI2sGpio2831Dtbo returns the overlays/i2s-gpio28-31.dtbo file.
//
//go:embed overlays/i2s-gpio28-31.dtbo
var OverlaysI2sGpio2831Dtbo []byte

// OverlaysI2sMasterDacDtbo returns the overlays/i2s-master-dac.dtbo file.
//
//go:embed overlays/i2s-master-dac.dtbo
var OverlaysI2sMasterDacDtbo []byte

// OverlaysIlitek251xDtbo returns the overlays/ilitek251x.dtbo file.
//
//go:embed overlays/ilitek251x.dtbo
var OverlaysIlitek251xDtbo []byte

// OverlaysImx219Dtbo returns the overlays/imx219.dtbo file.
//
//go:embed overlays/imx219.dtbo
var OverlaysImx219Dtbo []byte

// OverlaysImx258Dtbo returns the overlays/imx258.dtbo file.
//
//go:embed overlays/imx258.dtbo
var OverlaysImx258Dtbo []byte

// OverlaysImx283Dtbo returns the overlays/imx283.dtbo file.
//
//go:embed overlays/imx283.dtbo
var OverlaysImx283Dtbo []byte

// OverlaysImx290Dtbo returns the overlays/imx290.dtbo file.
//
//go:embed overlays/imx290.dtbo
var OverlaysImx290Dtbo []byte

// OverlaysImx296Dtbo returns the overlays/imx296.dtbo file.
//
//go:embed overlays/imx296.dtbo
var OverlaysImx296Dtbo []byte

// OverlaysImx327Dtbo returns the overlays/imx327.dtbo file.
//
//go:embed overlays/imx327.dtbo
var OverlaysImx327Dtbo []byte

// OverlaysImx335Dtbo returns the overlays/imx335.dtbo file.
//
//go:embed overlays/imx335.dtbo
var OverlaysImx335Dtbo []byte

// OverlaysImx378Dtbo returns the overlays/imx378.dtbo file.
//
//go:embed overlays/imx378.dtbo
var OverlaysImx378Dtbo []byte

// OverlaysImx415Dtbo returns the overlays/imx415.dtbo file.
//
//go:embed overlays/imx415.dtbo
var OverlaysImx415Dtbo []byte

// OverlaysImx462Dtbo returns the overlays/imx462.dtbo file.
//
//go:embed overlays/imx462.dtbo
var OverlaysImx462Dtbo []byte

// OverlaysImx477Dtbo returns the overlays/imx477.dtbo file.
//
//go:embed overlays/imx477.dtbo
var OverlaysImx477Dtbo []byte

// OverlaysImx500Pi5Dtbo returns the overlays/imx500-pi5.dtbo file.
//
//go:embed overlays/imx500-pi5.dtbo
var OverlaysImx500Pi5Dtbo []byte

// OverlaysImx500Dtbo returns the overlays/imx500.dtbo file.
//
//go:embed overlays/imx500.dtbo
var OverlaysImx500Dtbo []byte

// OverlaysImx519Dtbo returns the overlays/imx519.dtbo file.
//
//go:embed overlays/imx519.dtbo
var OverlaysImx519Dtbo []byte

// OverlaysImx708Dtbo returns the overlays/imx708.dtbo file.
//
//go:embed overlays/imx708.dtbo
var OverlaysImx708Dtbo []byte

// OverlaysInterludeaudioAnalogDtbo returns the overlays/interludeaudio-analog.dtbo file.
//
//go:embed overlays/interludeaudio-analog.dtbo
var OverlaysInterludeaudioAnalogDtbo []byte

// OverlaysInterludeaudioDigitalDtbo returns the overlays/interludeaudio-digital.dtbo file.
//
//go:embed overlays/interludeaudio-digital.dtbo
var OverlaysInterludeaudioDigitalDtbo []byte

// OverlaysIqaudioCodecDtbo returns the overlays/iqaudio-codec.dtbo file.
//
//go:embed overlays/iqaudio-codec.dtbo
var OverlaysIqaudioCodecDtbo []byte

// OverlaysIqaudioDacDtbo returns the overlays/iqaudio-dac.dtbo file.
//
//go:embed overlays/iqaudio-dac.dtbo
var OverlaysIqaudioDacDtbo []byte

// OverlaysIqaudioDacplusDtbo returns the overlays/iqaudio-dacplus.dtbo file.
//
//go:embed overlays/iqaudio-dacplus.dtbo
var OverlaysIqaudioDacplusDtbo []byte

// OverlaysIqaudioDigiWm8804AudioDtbo returns the overlays/iqaudio-digi-wm8804-audio.dtbo file.
//
//go:embed overlays/iqaudio-digi-wm8804-audio.dtbo
var OverlaysIqaudioDigiWm8804AudioDtbo []byte

// OverlaysIqs550Dtbo returns the overlays/iqs550.dtbo file.
//
//go:embed overlays/iqs550.dtbo
var OverlaysIqs550Dtbo []byte

// OverlaysIrs1125Dtbo returns the overlays/irs1125.dtbo file.
//
//go:embed overlays/irs1125.dtbo
var OverlaysIrs1125Dtbo []byte

// OverlaysJedecSpiNorDtbo returns the overlays/jedec-spi-nor.dtbo file.
//
//go:embed overlays/jedec-spi-nor.dtbo
var OverlaysJedecSpiNorDtbo []byte

// OverlaysJustboomBothDtbo returns the overlays/justboom-both.dtbo file.
//
//go:embed overlays/justboom-both.dtbo
var OverlaysJustboomBothDtbo []byte

// OverlaysJustboomDacDtbo returns the overlays/justboom-dac.dtbo file.
//
//go:embed overlays/justboom-dac.dtbo
var OverlaysJustboomDacDtbo []byte

// OverlaysJustboomDigiDtbo returns the overlays/justboom-digi.dtbo file.
//
//go:embed overlays/justboom-digi.dtbo
var OverlaysJustboomDigiDtbo []byte

// OverlaysLtc294xDtbo returns the overlays/ltc294x.dtbo file.
//
//go:embed overlays/ltc294x.dtbo
var OverlaysLtc294xDtbo []byte

// OverlaysMax98357aDtbo returns the overlays/max98357a.dtbo file.
//
//go:embed overlays/max98357a.dtbo
var OverlaysMax98357aDtbo []byte

// OverlaysMaxthermDtbo returns the overlays/maxtherm.dtbo file.
//
//go:embed overlays/maxtherm.dtbo
var OverlaysMaxthermDtbo []byte

// OverlaysMbedDacDtbo returns the overlays/mbed-dac.dtbo file.
//
//go:embed overlays/mbed-dac.dtbo
var OverlaysMbedDacDtbo []byte

// OverlaysMcp23017Dtbo returns the overlays/mcp23017.dtbo file.
//
//go:embed overlays/mcp23017.dtbo
var OverlaysMcp23017Dtbo []byte

// OverlaysMcp23s17Dtbo returns the overlays/mcp23s17.dtbo file.
//
//go:embed overlays/mcp23s17.dtbo
var OverlaysMcp23s17Dtbo []byte

// OverlaysMcp2515Can0Dtbo returns the overlays/mcp2515-can0.dtbo file.
//
//go:embed overlays/mcp2515-can0.dtbo
var OverlaysMcp2515Can0Dtbo []byte

// OverlaysMcp2515Can1Dtbo returns the overlays/mcp2515-can1.dtbo file.
//
//go:embed overlays/mcp2515-can1.dtbo
var OverlaysMcp2515Can1Dtbo []byte

// OverlaysMcp2515Dtbo returns the overlays/mcp2515.dtbo file.
//
//go:embed overlays/mcp2515.dtbo
var OverlaysMcp2515Dtbo []byte

// OverlaysMcp251xfdDtbo returns the overlays/mcp251xfd.dtbo file.
//
//go:embed overlays/mcp251xfd.dtbo
var OverlaysMcp251xfdDtbo []byte

// OverlaysMcp3008Dtbo returns the overlays/mcp3008.dtbo file.
//
//go:embed overlays/mcp3008.dtbo
var OverlaysMcp3008Dtbo []byte

// OverlaysMcp3202Dtbo returns the overlays/mcp3202.dtbo file.
//
//go:embed overlays/mcp3202.dtbo
var OverlaysMcp3202Dtbo []byte

// OverlaysMcp342xDtbo returns the overlays/mcp342x.dtbo file.
//
//go:embed overlays/mcp342x.dtbo
var OverlaysMcp342xDtbo []byte

// OverlaysMediaCenterDtbo returns the overlays/media-center.dtbo file.
//
//go:embed overlays/media-center.dtbo
var OverlaysMediaCenterDtbo []byte

// OverlaysMerusAmpDtbo returns the overlays/merus-amp.dtbo file.
//
//go:embed overlays/merus-amp.dtbo
var OverlaysMerusAmpDtbo []byte

// OverlaysMidiUart0Pi5Dtbo returns the overlays/midi-uart0-pi5.dtbo file.
//
//go:embed overlays/midi-uart0-pi5.dtbo
var OverlaysMidiUart0Pi5Dtbo []byte

// OverlaysMidiUart0Dtbo returns the overlays/midi-uart0.dtbo file.
//
//go:embed overlays/midi-uart0.dtbo
var OverlaysMidiUart0Dtbo []byte

// OverlaysMidiUart1Pi5Dtbo returns the overlays/midi-uart1-pi5.dtbo file.
//
//go:embed overlays/midi-uart1-pi5.dtbo
var OverlaysMidiUart1Pi5Dtbo []byte

// OverlaysMidiUart1Dtbo returns the overlays/midi-uart1.dtbo file.
//
//go:embed overlays/midi-uart1.dtbo
var OverlaysMidiUart1Dtbo []byte

// OverlaysMidiUart2Pi5Dtbo returns the overlays/midi-uart2-pi5.dtbo file.
//
//go:embed overlays/midi-uart2-pi5.dtbo
var OverlaysMidiUart2Pi5Dtbo []byte

// OverlaysMidiUart2Dtbo returns the overlays/midi-uart2.dtbo file.
//
//go:embed overlays/midi-uart2.dtbo
var OverlaysMidiUart2Dtbo []byte

// OverlaysMidiUart3Pi5Dtbo returns the overlays/midi-uart3-pi5.dtbo file.
//
//go:embed overlays/midi-uart3-pi5.dtbo
var OverlaysMidiUart3Pi5Dtbo []byte

// OverlaysMidiUart3Dtbo returns the overlays/midi-uart3.dtbo file.
//
//go:embed overlays/midi-uart3.dtbo
var OverlaysMidiUart3Dtbo []byte

// OverlaysMidiUart4Pi5Dtbo returns the overlays/midi-uart4-pi5.dtbo file.
//
//go:embed overlays/midi-uart4-pi5.dtbo
var OverlaysMidiUart4Pi5Dtbo []byte

// OverlaysMidiUart4Dtbo returns the overlays/midi-uart4.dtbo file.
//
//go:embed overlays/midi-uart4.dtbo
var OverlaysMidiUart4Dtbo []byte

// OverlaysMidiUart5Dtbo returns the overlays/midi-uart5.dtbo file.
//
//go:embed overlays/midi-uart5.dtbo
var OverlaysMidiUart5Dtbo []byte

// OverlaysMinipitft13Dtbo returns the overlays/minipitft13.dtbo file.
//
//go:embed overlays/minipitft13.dtbo
var OverlaysMinipitft13Dtbo []byte

// OverlaysMipiDbiSpiDtbo returns the overlays/mipi-dbi-spi.dtbo file.
//
//go:embed overlays/mipi-dbi-spi.dtbo
var OverlaysMipiDbiSpiDtbo []byte

// OverlaysMira220Dtbo returns the overlays/mira220.dtbo file.
//
//go:embed overlays/mira220.dtbo
var OverlaysMira220Dtbo []byte

// OverlaysMlx90640Dtbo returns the overlays/mlx90640.dtbo file.
//
//go:embed overlays/mlx90640.dtbo
var OverlaysMlx90640Dtbo []byte

// OverlaysMmcDtbo returns the overlays/mmc.dtbo file.
//
//go:embed overlays/mmc.dtbo
var OverlaysMmcDtbo []byte

// OverlaysMz61581Dtbo returns the overlays/mz61581.dtbo file.
//
//go:embed overlays/mz61581.dtbo
var OverlaysMz61581Dtbo []byte

// OverlaysOv2311Dtbo returns the overlays/ov2311.dtbo file.
//
//go:embed overlays/ov2311.dtbo
var OverlaysOv2311Dtbo []byte

// OverlaysOv5647Dtbo returns the overlays/ov5647.dtbo file.
//
//go:embed overlays/ov5647.dtbo
var OverlaysOv5647Dtbo []byte

// OverlaysOv64a40Dtbo returns the overlays/ov64a40.dtbo file.
//
//go:embed overlays/ov64a40.dtbo
var OverlaysOv64a40Dtbo []byte

// OverlaysOv7251Dtbo returns the overlays/ov7251.dtbo file.
//
//go:embed overlays/ov7251.dtbo
var OverlaysOv7251Dtbo []byte

// OverlaysOv9281Dtbo returns the overlays/ov9281.dtbo file.
//
//go:embed overlays/ov9281.dtbo
var OverlaysOv9281Dtbo []byte

// OverlaysOverlayMapDtb returns the overlays/overlay_map.dtb file.
//
//go:embed overlays/overlay_map.dtb
var OverlaysOverlayMapDtb []byte

// OverlaysPapirusDtbo returns the overlays/papirus.dtbo file.
//
//go:embed overlays/papirus.dtbo
var OverlaysPapirusDtbo []byte

// OverlaysPca953xDtbo returns the overlays/pca953x.dtbo file.
//
//go:embed overlays/pca953x.dtbo
var OverlaysPca953xDtbo []byte

// OverlaysPcf857xDtbo returns the overlays/pcf857x.dtbo file.
//
//go:embed overlays/pcf857x.dtbo
var OverlaysPcf857xDtbo []byte

// OverlaysPcie32bitDmaPi5Dtbo returns the overlays/pcie-32bit-dma-pi5.dtbo file.
//
//go:embed overlays/pcie-32bit-dma-pi5.dtbo
var OverlaysPcie32bitDmaPi5Dtbo []byte

// OverlaysPcie32bitDmaDtbo returns the overlays/pcie-32bit-dma.dtbo file.
//
//go:embed overlays/pcie-32bit-dma.dtbo
var OverlaysPcie32bitDmaDtbo []byte

// OverlaysPciex1CompatPi5Dtbo returns the overlays/pciex1-compat-pi5.dtbo file.
//
//go:embed overlays/pciex1-compat-pi5.dtbo
var OverlaysPciex1CompatPi5Dtbo []byte

// OverlaysPibellDtbo returns the overlays/pibell.dtbo file.
//
//go:embed overlays/pibell.dtbo
var OverlaysPibellDtbo []byte

// OverlaysPifacedigitalDtbo returns the overlays/pifacedigital.dtbo file.
//
//go:embed overlays/pifacedigital.dtbo
var OverlaysPifacedigitalDtbo []byte

// OverlaysPifi40Dtbo returns the overlays/pifi-40.dtbo file.
//
//go:embed overlays/pifi-40.dtbo
var OverlaysPifi40Dtbo []byte

// OverlaysPifiDacHdDtbo returns the overlays/pifi-dac-hd.dtbo file.
//
//go:embed overlays/pifi-dac-hd.dtbo
var OverlaysPifiDacHdDtbo []byte

// OverlaysPifiDacZeroDtbo returns the overlays/pifi-dac-zero.dtbo file.
//
//go:embed overlays/pifi-dac-zero.dtbo
var OverlaysPifiDacZeroDtbo []byte

// OverlaysPifiMini210Dtbo returns the overlays/pifi-mini-210.dtbo file.
//
//go:embed overlays/pifi-mini-210.dtbo
var OverlaysPifiMini210Dtbo []byte

// OverlaysPiglowDtbo returns the overlays/piglow.dtbo file.
//
//go:embed overlays/piglow.dtbo
var OverlaysPiglowDtbo []byte

// OverlaysPimidiDtbo returns the overlays/pimidi.dtbo file.
//
//go:embed overlays/pimidi.dtbo
var OverlaysPimidiDtbo []byte

// OverlaysPineboardsHatAiDtbo returns the overlays/pineboards-hat-ai.dtbo file.
//
//go:embed overlays/pineboards-hat-ai.dtbo
var OverlaysPineboardsHatAiDtbo []byte

// OverlaysPineboardsHatdrivePoePlusDtbo returns the overlays/pineboards-hatdrive-poe-plus.dtbo file.
//
//go:embed overlays/pineboards-hatdrive-poe-plus.dtbo
var OverlaysPineboardsHatdrivePoePlusDtbo []byte

// OverlaysPiscreenDtbo returns the overlays/piscreen.dtbo file.
//
//go:embed overlays/piscreen.dtbo
var OverlaysPiscreenDtbo []byte

// OverlaysPiscreen2rDtbo returns the overlays/piscreen2r.dtbo file.
//
//go:embed overlays/piscreen2r.dtbo
var OverlaysPiscreen2rDtbo []byte

// OverlaysPisoundMicroDtbo returns the overlays/pisound-micro.dtbo file.
//
//go:embed overlays/pisound-micro.dtbo
var OverlaysPisoundMicroDtbo []byte

// OverlaysPisoundPi5Dtbo returns the overlays/pisound-pi5.dtbo file.
//
//go:embed overlays/pisound-pi5.dtbo
var OverlaysPisoundPi5Dtbo []byte

// OverlaysPisoundDtbo returns the overlays/pisound.dtbo file.
//
//go:embed overlays/pisound.dtbo
var OverlaysPisoundDtbo []byte

// OverlaysPitft22Dtbo returns the overlays/pitft22.dtbo file.
//
//go:embed overlays/pitft22.dtbo
var OverlaysPitft22Dtbo []byte

// OverlaysPitft28CapacitiveDtbo returns the overlays/pitft28-capacitive.dtbo file.
//
//go:embed overlays/pitft28-capacitive.dtbo
var OverlaysPitft28CapacitiveDtbo []byte

// OverlaysPitft28ResistiveDtbo returns the overlays/pitft28-resistive.dtbo file.
//
//go:embed overlays/pitft28-resistive.dtbo
var OverlaysPitft28ResistiveDtbo []byte

// OverlaysPitft35ResistiveDtbo returns the overlays/pitft35-resistive.dtbo file.
//
//go:embed overlays/pitft35-resistive.dtbo
var OverlaysPitft35ResistiveDtbo []byte

// OverlaysPivisionDtbo returns the overlays/pivision.dtbo file.
//
//go:embed overlays/pivision.dtbo
var OverlaysPivisionDtbo []byte

// OverlaysPpsGpioDtbo returns the overlays/pps-gpio.dtbo file.
//
//go:embed overlays/pps-gpio.dtbo
var OverlaysPpsGpioDtbo []byte

// OverlaysProtoCodecDtbo returns the overlays/proto-codec.dtbo file.
//
//go:embed overlays/proto-codec.dtbo
var OverlaysProtoCodecDtbo []byte

// OverlaysPwm2chanDtbo returns the overlays/pwm-2chan.dtbo file.
//
//go:embed overlays/pwm-2chan.dtbo
var OverlaysPwm2chanDtbo []byte

// OverlaysPwmGpioFanDtbo returns the overlays/pwm-gpio-fan.dtbo file.
//
//go:embed overlays/pwm-gpio-fan.dtbo
var OverlaysPwmGpioFanDtbo []byte

// OverlaysPwmGpioDtbo returns the overlays/pwm-gpio.dtbo file.
//
//go:embed overlays/pwm-gpio.dtbo
var OverlaysPwmGpioDtbo []byte

// OverlaysPwmIrTxDtbo returns the overlays/pwm-ir-tx.dtbo file.
//
//go:embed overlays/pwm-ir-tx.dtbo
var OverlaysPwmIrTxDtbo []byte

// OverlaysPwmPioDtbo returns the overlays/pwm-pio.dtbo file.
//
//go:embed overlays/pwm-pio.dtbo
var OverlaysPwmPioDtbo []byte

// OverlaysPwmDtbo returns the overlays/pwm.dtbo file.
//
//go:embed overlays/pwm.dtbo
var OverlaysPwmDtbo []byte

// OverlaysPwm1Dtbo returns the overlays/pwm1.dtbo file.
//
//go:embed overlays/pwm1.dtbo
var OverlaysPwm1Dtbo []byte

// OverlaysQca7000Uart0Dtbo returns the overlays/qca7000-uart0.dtbo file.
//
//go:embed overlays/qca7000-uart0.dtbo
var OverlaysQca7000Uart0Dtbo []byte

// OverlaysQca7000Dtbo returns the overlays/qca7000.dtbo file.
//
//go:embed overlays/qca7000.dtbo
var OverlaysQca7000Dtbo []byte

// OverlaysRamoopsPi4Dtbo returns the overlays/ramoops-pi4.dtbo file.
//
//go:embed overlays/ramoops-pi4.dtbo
var OverlaysRamoopsPi4Dtbo []byte

// OverlaysRamoopsDtbo returns the overlays/ramoops.dtbo file.
//
//go:embed overlays/ramoops.dtbo
var OverlaysRamoopsDtbo []byte

// OverlaysRootmasterDtbo returns the overlays/rootmaster.dtbo file.
//
//go:embed overlays/rootmaster.dtbo
var OverlaysRootmasterDtbo []byte

// OverlaysRotaryEncoderDtbo returns the overlays/rotary-encoder.dtbo file.
//
//go:embed overlays/rotary-encoder.dtbo
var OverlaysRotaryEncoderDtbo []byte

// OverlaysRpiBacklightDtbo returns the overlays/rpi-backlight.dtbo file.
//
//go:embed overlays/rpi-backlight.dtbo
var OverlaysRpiBacklightDtbo []byte

// OverlaysRpiCodeczzeroDtbo returns the overlays/rpi-codeczero.dtbo file.
//
//go:embed overlays/rpi-codeczero.dtbo
var OverlaysRpiCodeczzeroDtbo []byte

// OverlaysRpiDacplusDtbo returns the overlays/rpi-dacplus.dtbo file.
//
//go:embed overlays/rpi-dacplus.dtbo
var OverlaysRpiDacplusDtbo []byte

// OverlaysRpiDacproDtbo returns the overlays/rpi-dacpro.dtbo file.
//
//go:embed overlays/rpi-dacpro.dtbo
var OverlaysRpiDacproDtbo []byte

// OverlaysRpiDigiampplusDtbo returns the overlays/rpi-digiampplus.dtbo file.
//
//go:embed overlays/rpi-digiampplus.dtbo
var OverlaysRpiDigiampplusDtbo []byte

// OverlaysRpiFt5406Dtbo returns the overlays/rpi-ft5406.dtbo file.
//
//go:embed overlays/rpi-ft5406.dtbo
var OverlaysRpiFt5406Dtbo []byte

// OverlaysRpiFwUartDtbo returns the overlays/rpi-fw-uart.dtbo file.
//
//go:embed overlays/rpi-fw-uart.dtbo
var OverlaysRpiFwUartDtbo []byte

// OverlaysRpiPoeDtbo returns the overlays/rpi-poe.dtbo file.
//
//go:embed overlays/rpi-poe.dtbo
var OverlaysRpiPoeDtbo []byte

// OverlaysRpiSenseV2Dtbo returns the overlays/rpi-sense-v2.dtbo file.
//
//go:embed overlays/rpi-sense-v2.dtbo
var OverlaysRpiSenseV2Dtbo []byte

// OverlaysRpiSenseDtbo returns the overlays/rpi-sense.dtbo file.
//
//go:embed overlays/rpi-sense.dtbo
var OverlaysRpiSenseDtbo []byte

// OverlaysRpiTvDtbo returns the overlays/rpi-tv.dtbo file.
//
//go:embed overlays/rpi-tv.dtbo
var OverlaysRpiTvDtbo []byte

// OverlaysRraDigidac1Wm8741AudioDtbo returns the overlays/rra-digidac1-wm8741-audio.dtbo file.
//
//go:embed overlays/rra-digidac1-wm8741-audio.dtbo
var OverlaysRraDigidac1Wm8741AudioDtbo []byte

// OverlaysSainsmart18Dtbo returns the overlays/sainsmart18.dtbo file.
//
//go:embed overlays/sainsmart18.dtbo
var OverlaysSainsmart18Dtbo []byte

// OverlaysSc16is750I2cDtbo returns the overlays/sc16is750-i2c.dtbo file.
//
//go:embed overlays/sc16is750-i2c.dtbo
var OverlaysSc16is750I2cDtbo []byte

// OverlaysSc16is752I2cDtbo returns the overlays/sc16is752-i2c.dtbo file.
//
//go:embed overlays/sc16is752-i2c.dtbo
var OverlaysSc16is752I2cDtbo []byte

// OverlaysSc16is75xSpiDtbo returns the overlays/sc16is75x-spi.dtbo file.
//
//go:embed overlays/sc16is75x-spi.dtbo
var OverlaysSc16is75xSpiDtbo []byte

// OverlaysSdhostDtbo returns the overlays/sdhost.dtbo file.
//
//go:embed overlays/sdhost.dtbo
var OverlaysSdhostDtbo []byte

// OverlaysSdioPi5Dtbo returns the overlays/sdio-pi5.dtbo file.
//
//go:embed overlays/sdio-pi5.dtbo
var OverlaysSdioPi5Dtbo []byte

// OverlaysSdioDtbo returns the overlays/sdio.dtbo file.
//
//go:embed overlays/sdio.dtbo
var OverlaysSdioDtbo []byte

// OverlaysSeeedCanFdHatV1Dtbo returns the overlays/seeed-can-fd-hat-v1.dtbo file.
//
//go:embed overlays/seeed-can-fd-hat-v1.dtbo
var OverlaysSeeedCanFdHatV1Dtbo []byte

// OverlaysSeeedCanFdHatV2Dtbo returns the overlays/seeed-can-fd-hat-v2.dtbo file.
//
//go:embed overlays/seeed-can-fd-hat-v2.dtbo
var OverlaysSeeedCanFdHatV2Dtbo []byte

// OverlaysSh1106SpiDtbo returns the overlays/sh1106-spi.dtbo file.
//
//go:embed overlays/sh1106-spi.dtbo
var OverlaysSh1106SpiDtbo []byte

// OverlaysSi446xSpi0Dtbo returns the overlays/si446x-spi0.dtbo file.
//
//go:embed overlays/si446x-spi0.dtbo
var OverlaysSi446xSpi0Dtbo []byte

// OverlaysSmiDevDtbo returns the overlays/smi-dev.dtbo file.
//
//go:embed overlays/smi-dev.dtbo
var OverlaysSmiDevDtbo []byte

// OverlaysSmiNandDtbo returns the overlays/smi-nand.dtbo file.
//
//go:embed overlays/smi-nand.dtbo
var OverlaysSmiNandDtbo []byte

// OverlaysSmiDtbo returns the overlays/smi.dtbo file.
//
//go:embed overlays/smi.dtbo
var OverlaysSmiDtbo []byte

// OverlaysSpiGpio3539Dtbo returns the overlays/spi-gpio35-39.dtbo file.
//
//go:embed overlays/spi-gpio35-39.dtbo
var OverlaysSpiGpio3539Dtbo []byte

// OverlaysSpiGpio4045Dtbo returns the overlays/spi-gpio40-45.dtbo file.
//
//go:embed overlays/spi-gpio40-45.dtbo
var OverlaysSpiGpio4045Dtbo []byte

// OverlaysSpiRtcDtbo returns the overlays/spi-rtc.dtbo file.
//
//go:embed overlays/spi-rtc.dtbo
var OverlaysSpiRtcDtbo []byte

// OverlaysSpi00csDtbo returns the overlays/spi0-0cs.dtbo file.
//
//go:embed overlays/spi0-0cs.dtbo
var OverlaysSpi00csDtbo []byte

// OverlaysSpi01csInvertedDtbo returns the overlays/spi0-1cs-inverted.dtbo file.
//
//go:embed overlays/spi0-1cs-inverted.dtbo
var OverlaysSpi01csInvertedDtbo []byte

// OverlaysSpi01csDtbo returns the overlays/spi0-1cs.dtbo file.
//
//go:embed overlays/spi0-1cs.dtbo
var OverlaysSpi01csDtbo []byte

// OverlaysSpi02csDtbo returns the overlays/spi0-2cs.dtbo file.
//
//go:embed overlays/spi0-2cs.dtbo
var OverlaysSpi02csDtbo []byte

// OverlaysSpi11csDtbo returns the overlays/spi1-1cs.dtbo file.
//
//go:embed overlays/spi1-1cs.dtbo
var OverlaysSpi11csDtbo []byte

// OverlaysSpi12csDtbo returns the overlays/spi1-2cs.dtbo file.
//
//go:embed overlays/spi1-2cs.dtbo
var OverlaysSpi12csDtbo []byte

// OverlaysSpi13csDtbo returns the overlays/spi1-3cs.dtbo file.
//
//go:embed overlays/spi1-3cs.dtbo
var OverlaysSpi13csDtbo []byte

// OverlaysSpi21csPi5Dtbo returns the overlays/spi2-1cs-pi5.dtbo file.
//
//go:embed overlays/spi2-1cs-pi5.dtbo
var OverlaysSpi21csPi5Dtbo []byte

// OverlaysSpi21csDtbo returns the overlays/spi2-1cs.dtbo file.
//
//go:embed overlays/spi2-1cs.dtbo
var OverlaysSpi21csDtbo []byte

// OverlaysSpi22csPi5Dtbo returns the overlays/spi2-2cs-pi5.dtbo file.
//
//go:embed overlays/spi2-2cs-pi5.dtbo
var OverlaysSpi22csPi5Dtbo []byte

// OverlaysSpi22csDtbo returns the overlays/spi2-2cs.dtbo file.
//
//go:embed overlays/spi2-2cs.dtbo
var OverlaysSpi22csDtbo []byte

// OverlaysSpi23csDtbo returns the overlays/spi2-3cs.dtbo file.
//
//go:embed overlays/spi2-3cs.dtbo
var OverlaysSpi23csDtbo []byte

// OverlaysSpi31csPi5Dtbo returns the overlays/spi3-1cs-pi5.dtbo file.
//
//go:embed overlays/spi3-1cs-pi5.dtbo
var OverlaysSpi31csPi5Dtbo []byte

// OverlaysSpi31csDtbo returns the overlays/spi3-1cs.dtbo file.
//
//go:embed overlays/spi3-1cs.dtbo
var OverlaysSpi31csDtbo []byte

// OverlaysSpi32csPi5Dtbo returns the overlays/spi3-2cs-pi5.dtbo file.
//
//go:embed overlays/spi3-2cs-pi5.dtbo
var OverlaysSpi32csPi5Dtbo []byte

// OverlaysSpi32csDtbo returns the overlays/spi3-2cs.dtbo file.
//
//go:embed overlays/spi3-2cs.dtbo
var OverlaysSpi32csDtbo []byte

// OverlaysSpi41csDtbo returns the overlays/spi4-1cs.dtbo file.
//
//go:embed overlays/spi4-1cs.dtbo
var OverlaysSpi41csDtbo []byte

// OverlaysSpi42csDtbo returns the overlays/spi4-2cs.dtbo file.
//
//go:embed overlays/spi4-2cs.dtbo
var OverlaysSpi42csDtbo []byte

// OverlaysSpi51csPi5Dtbo returns the overlays/spi5-1cs-pi5.dtbo file.
//
//go:embed overlays/spi5-1cs-pi5.dtbo
var OverlaysSpi51csPi5Dtbo []byte

// OverlaysSpi51csDtbo returns the overlays/spi5-1cs.dtbo file.
//
//go:embed overlays/spi5-1cs.dtbo
var OverlaysSpi51csDtbo []byte

// OverlaysSpi52csPi5Dtbo returns the overlays/spi5-2cs-pi5.dtbo file.
//
//go:embed overlays/spi5-2cs-pi5.dtbo
var OverlaysSpi52csPi5Dtbo []byte

// OverlaysSpi52csDtbo returns the overlays/spi5-2cs.dtbo file.
//
//go:embed overlays/spi5-2cs.dtbo
var OverlaysSpi52csDtbo []byte

// OverlaysSpi61csDtbo returns the overlays/spi6-1cs.dtbo file.
//
//go:embed overlays/spi6-1cs.dtbo
var OverlaysSpi61csDtbo []byte

// OverlaysSpi62csDtbo returns the overlays/spi6-2cs.dtbo file.
//
//go:embed overlays/spi6-2cs.dtbo
var OverlaysSpi62csDtbo []byte

// OverlaysSsd1306SpiDtbo returns the overlays/ssd1306-spi.dtbo file.
//
//go:embed overlays/ssd1306-spi.dtbo
var OverlaysSsd1306SpiDtbo []byte

// OverlaysSsd1306Dtbo returns the overlays/ssd1306.dtbo file.
//
//go:embed overlays/ssd1306.dtbo
var OverlaysSsd1306Dtbo []byte

// OverlaysSsd1327SpiDtbo returns the overlays/ssd1327-spi.dtbo file.
//
//go:embed overlays/ssd1327-spi.dtbo
var OverlaysSsd1327SpiDtbo []byte

// OverlaysSsd1331SpiDtbo returns the overlays/ssd1331-spi.dtbo file.
//
//go:embed overlays/ssd1331-spi.dtbo
var OverlaysSsd1331SpiDtbo []byte

// OverlaysSsd1351SpiDtbo returns the overlays/ssd1351-spi.dtbo file.
//
//go:embed overlays/ssd1351-spi.dtbo
var OverlaysSsd1351SpiDtbo []byte

// OverlaysSunfounderPipower3Dtbo returns the overlays/sunfounder-pipower3.dtbo file.
//
//go:embed overlays/sunfounder-pipower3.dtbo
var OverlaysSunfounderPipower3Dtbo []byte

// OverlaysSunfounderPironman5Dtbo returns the overlays/sunfounder-pironman5.dtbo file.
//
//go:embed overlays/sunfounder-pironman5.dtbo
var OverlaysSunfounderPironman5Dtbo []byte

// OverlaysSuperaudioboardDtbo returns the overlays/superaudioboard.dtbo file.
//
//go:embed overlays/superaudioboard.dtbo
var OverlaysSuperaudioboardDtbo []byte

// OverlaysSx150xDtbo returns the overlays/sx150x.dtbo file.
//
//go:embed overlays/sx150x.dtbo
var OverlaysSx150xDtbo []byte

// OverlaysTc358743AudioDtbo returns the overlays/tc358743-audio.dtbo file.
//
//go:embed overlays/tc358743-audio.dtbo
var OverlaysTc358743AudioDtbo []byte

// OverlaysTc358743Pi5Dtbo returns the overlays/tc358743-pi5.dtbo file.
//
//go:embed overlays/tc358743-pi5.dtbo
var OverlaysTc358743Pi5Dtbo []byte

// OverlaysTc358743Dtbo returns the overlays/tc358743.dtbo file.
//
//go:embed overlays/tc358743.dtbo
var OverlaysTc358743Dtbo []byte

// OverlaysTinylcd35Dtbo returns the overlays/tinylcd35.dtbo file.
//
//go:embed overlays/tinylcd35.dtbo
var OverlaysTinylcd35Dtbo []byte

// OverlaysTpmSlb9670Dtbo returns the overlays/tpm-slb9670.dtbo file.
//
//go:embed overlays/tpm-slb9670.dtbo
var OverlaysTpmSlb9670Dtbo []byte

// OverlaysTpmSlb9673Dtbo returns the overlays/tpm-slb9673.dtbo file.
//
//go:embed overlays/tpm-slb9673.dtbo
var OverlaysTpmSlb9673Dtbo []byte

// OverlaysUart0Pi5Dtbo returns the overlays/uart0-pi5.dtbo file.
//
//go:embed overlays/uart0-pi5.dtbo
var OverlaysUart0Pi5Dtbo []byte

// OverlaysUart0Dtbo returns the overlays/uart0.dtbo file.
//
//go:embed overlays/uart0.dtbo
var OverlaysUart0Dtbo []byte

// OverlaysUart1Pi5Dtbo returns the overlays/uart1-pi5.dtbo file.
//
//go:embed overlays/uart1-pi5.dtbo
var OverlaysUart1Pi5Dtbo []byte

// OverlaysUart1Dtbo returns the overlays/uart1.dtbo file.
//
//go:embed overlays/uart1.dtbo
var OverlaysUart1Dtbo []byte

// OverlaysUart2Pi5Dtbo returns the overlays/uart2-pi5.dtbo file.
//
//go:embed overlays/uart2-pi5.dtbo
var OverlaysUart2Pi5Dtbo []byte

// OverlaysUart2Dtbo returns the overlays/uart2.dtbo file.
//
//go:embed overlays/uart2.dtbo
var OverlaysUart2Dtbo []byte

// OverlaysUart3Pi5Dtbo returns the overlays/uart3-pi5.dtbo file.
//
//go:embed overlays/uart3-pi5.dtbo
var OverlaysUart3Pi5Dtbo []byte

// OverlaysUart3Dtbo returns the overlays/uart3.dtbo file.
//
//go:embed overlays/uart3.dtbo
var OverlaysUart3Dtbo []byte

// OverlaysUart4Pi5Dtbo returns the overlays/uart4-pi5.dtbo file.
//
//go:embed overlays/uart4-pi5.dtbo
var OverlaysUart4Pi5Dtbo []byte

// OverlaysUart4Dtbo returns the overlays/uart4.dtbo file.
//
//go:embed overlays/uart4.dtbo
var OverlaysUart4Dtbo []byte

// OverlaysUart5Dtbo returns the overlays/uart5.dtbo file.
//
//go:embed overlays/uart5.dtbo
var OverlaysUart5Dtbo []byte

// OverlaysUdrcDtbo returns the overlays/udrc.dtbo file.
//
//go:embed overlays/udrc.dtbo
var OverlaysUdrcDtbo []byte

// OverlaysUgreenDabboardDtbo returns the overlays/ugreen-dabboard.dtbo file.
//
//go:embed overlays/ugreen-dabboard.dtbo
var OverlaysUgreenDabboardDtbo []byte

// OverlaysUpstreamDtbo returns the overlays/upstream.dtbo file.
//
//go:embed overlays/upstream.dtbo
var OverlaysUpstreamDtbo []byte

// OverlaysVc4FkmsV3dPi4Dtbo returns the overlays/vc4-fkms-v3d-pi4.dtbo file.
//
//go:embed overlays/vc4-fkms-v3d-pi4.dtbo
var OverlaysVc4FkmsV3dPi4Dtbo []byte

// OverlaysVc4FkmsV3dDtbo returns the overlays/vc4-fkms-v3d.dtbo file.
//
//go:embed overlays/vc4-fkms-v3d.dtbo
var OverlaysVc4FkmsV3dDtbo []byte

// OverlaysVc4KmsDpiGenericDtbo returns the overlays/vc4-kms-dpi-generic.dtbo file.
//
//go:embed overlays/vc4-kms-dpi-generic.dtbo
var OverlaysVc4KmsDpiGenericDtbo []byte

// OverlaysVc4KmsDpiHyperpixel2rDtbo returns the overlays/vc4-kms-dpi-hyperpixel2r.dtbo file.
//
//go:embed overlays/vc4-kms-dpi-hyperpixel2r.dtbo
var OverlaysVc4KmsDpiHyperpixel2rDtbo []byte

// OverlaysVc4KmsDpiHyperpixel4Dtbo returns the overlays/vc4-kms-dpi-hyperpixel4.dtbo file.
//
//go:embed overlays/vc4-kms-dpi-hyperpixel4.dtbo
var OverlaysVc4KmsDpiHyperpixel4Dtbo []byte

// OverlaysVc4KmsDpiHyperpixel4sqDtbo returns the overlays/vc4-kms-dpi-hyperpixel4sq.dtbo file.
//
//go:embed overlays/vc4-kms-dpi-hyperpixel4sq.dtbo
var OverlaysVc4KmsDpiHyperpixel4sqDtbo []byte

// OverlaysVc4KmsDpiPanelDtbo returns the overlays/vc4-kms-dpi-panel.dtbo file.
//
//go:embed overlays/vc4-kms-dpi-panel.dtbo
var OverlaysVc4KmsDpiPanelDtbo []byte

// OverlaysVc4KmsDsi7inchDtbo returns the overlays/vc4-kms-dsi-7inch.dtbo file.
//
//go:embed overlays/vc4-kms-dsi-7inch.dtbo
var OverlaysVc4KmsDsi7inchDtbo []byte

// OverlaysVc4KmsDsiGenericDtbo returns the overlays/vc4-kms-dsi-generic.dtbo file.
//
//go:embed overlays/vc4-kms-dsi-generic.dtbo
var OverlaysVc4KmsDsiGenericDtbo []byte

// OverlaysVc4KmsDsiIli98815inchDtbo returns the overlays/vc4-kms-dsi-ili9881-5inch.dtbo file.
//
//go:embed overlays/vc4-kms-dsi-ili9881-5inch.dtbo
var OverlaysVc4KmsDsiIli98815inchDtbo []byte

// OverlaysVc4KmsDsiIli98817inchDtbo returns the overlays/vc4-kms-dsi-ili9881-7inch.dtbo file.
//
//go:embed overlays/vc4-kms-dsi-ili9881-7inch.dtbo
var OverlaysVc4KmsDsiIli98817inchDtbo []byte

// OverlaysVc4KmsDsiLt070me05000V2Dtbo returns the overlays/vc4-kms-dsi-lt070me05000-v2.dtbo file.
//
//go:embed overlays/vc4-kms-dsi-lt070me05000-v2.dtbo
var OverlaysVc4KmsDsiLt070me05000V2Dtbo []byte

// OverlaysVc4KmsDsiLt070me05000Dtbo returns the overlays/vc4-kms-dsi-lt070me05000.dtbo file.
//
//go:embed overlays/vc4-kms-dsi-lt070me05000.dtbo
var OverlaysVc4KmsDsiLt070me05000Dtbo []byte

// OverlaysVc4KmsDsiWaveshare800x480Dtbo returns the overlays/vc4-kms-dsi-waveshare-800x480.dtbo file.
//
//go:embed overlays/vc4-kms-dsi-waveshare-800x480.dtbo
var OverlaysVc4KmsDsiWaveshare800x480Dtbo []byte

// OverlaysVc4KmsDsiWavesharePanelV2Dtbo returns the overlays/vc4-kms-dsi-waveshare-panel-v2.dtbo file.
//
//go:embed overlays/vc4-kms-dsi-waveshare-panel-v2.dtbo
var OverlaysVc4KmsDsiWavesharePanelV2Dtbo []byte

// OverlaysVc4KmsDsiWavesharePanelDtbo returns the overlays/vc4-kms-dsi-waveshare-panel.dtbo file.
//
//go:embed overlays/vc4-kms-dsi-waveshare-panel.dtbo
var OverlaysVc4KmsDsiWavesharePanelDtbo []byte

// OverlaysVc4KmsKippah7inchDtbo returns the overlays/vc4-kms-kippah-7inch.dtbo file.
//
//go:embed overlays/vc4-kms-kippah-7inch.dtbo
var OverlaysVc4KmsKippah7inchDtbo []byte

// OverlaysVc4KmsV3dPi4Dtbo returns the overlays/vc4-kms-v3d-pi4.dtbo file.
//
//go:embed overlays/vc4-kms-v3d-pi4.dtbo
var OverlaysVc4KmsV3dPi4Dtbo []byte

// OverlaysVc4KmsV3dPi5Dtbo returns the overlays/vc4-kms-v3d-pi5.dtbo file.
//
//go:embed overlays/vc4-kms-v3d-pi5.dtbo
var OverlaysVc4KmsV3dPi5Dtbo []byte

// OverlaysVc4KmsV3dDtbo returns the overlays/vc4-kms-v3d.dtbo file.
//
//go:embed overlays/vc4-kms-v3d.dtbo
var OverlaysVc4KmsV3dDtbo []byte

// OverlaysVc4KmsVga666Dtbo returns the overlays/vc4-kms-vga666.dtbo file.
//
//go:embed overlays/vc4-kms-vga666.dtbo
var OverlaysVc4KmsVga666Dtbo []byte

// OverlaysVga666Dtbo returns the overlays/vga666.dtbo file.
//
//go:embed overlays/vga666.dtbo
var OverlaysVga666Dtbo []byte

// OverlaysVl805Dtbo returns the overlays/vl805.dtbo file.
//
//go:embed overlays/vl805.dtbo
var OverlaysVl805Dtbo []byte

// OverlaysW1GpioPi5Dtbo returns the overlays/w1-gpio-pi5.dtbo file.
//
//go:embed overlays/w1-gpio-pi5.dtbo
var OverlaysW1GpioPi5Dtbo []byte

// OverlaysW1GpioPullupPi5Dtbo returns the overlays/w1-gpio-pullup-pi5.dtbo file.
//
//go:embed overlays/w1-gpio-pullup-pi5.dtbo
var OverlaysW1GpioPullupPi5Dtbo []byte

// OverlaysW1GpioPullupDtbo returns the overlays/w1-gpio-pullup.dtbo file.
//
//go:embed overlays/w1-gpio-pullup.dtbo
var OverlaysW1GpioPullupDtbo []byte

// OverlaysW1GpioDtbo returns the overlays/w1-gpio.dtbo file.
//
//go:embed overlays/w1-gpio.dtbo
var OverlaysW1GpioDtbo []byte

// OverlaysW5500Dtbo returns the overlays/w5500.dtbo file.
//
//go:embed overlays/w5500.dtbo
var OverlaysW5500Dtbo []byte

// OverlaysWatterottDisplayDtbo returns the overlays/watterott-display.dtbo file.
//
//go:embed overlays/watterott-display.dtbo
var OverlaysWatterottDisplayDtbo []byte

// OverlaysWaveshareCanFdHatModeADtbo returns the overlays/waveshare-can-fd-hat-mode-a.dtbo file.
//
//go:embed overlays/waveshare-can-fd-hat-mode-a.dtbo
var OverlaysWaveshareCanFdHatModeADtbo []byte

// OverlaysWaveshareCanFdHatModeBDtbo returns the overlays/waveshare-can-fd-hat-mode-b.dtbo file.
//
//go:embed overlays/waveshare-can-fd-hat-mode-b.dtbo
var OverlaysWaveshareCanFdHatModeBDtbo []byte

// OverlaysWifimacDtbo returns the overlays/wifimac.dtbo file.
//
//go:embed overlays/wifimac.dtbo
var OverlaysWifimacDtbo []byte

// OverlaysWittypiDtbo returns the overlays/wittypi.dtbo file.
//
//go:embed overlays/wittypi.dtbo
var OverlaysWittypiDtbo []byte

// OverlaysWm8960SoundcardDtbo returns the overlays/wm8960-soundcard.dtbo file.
//
//go:embed overlays/wm8960-soundcard.dtbo
var OverlaysWm8960SoundcardDtbo []byte

// OverlaysWs2812PioDtbo returns the overlays/ws2812-pio.dtbo file.
//
//go:embed overlays/ws2812-pio.dtbo
var OverlaysWs2812PioDtbo []byte

// FirmwareBrcmBrcmfmac43455SdioBin returns the firmware/brcm/brcmfmac43455-sdio.bin file.
//
//go:embed firmware/brcm/brcmfmac43455-sdio.bin
var FirmwareBrcmBrcmfmac43455SdioBin []byte

// FirmwareBrcmBrcmfmac43455SdioTxt returns the firmware/brcm/brcmfmac43455-sdio.txt file.
//
//go:embed firmware/brcm/brcmfmac43455-sdio.txt
var FirmwareBrcmBrcmfmac43455SdioTxt []byte

// FirmwareBrcmBrcmfmac43455SdioClmBlob returns the firmware/brcm/brcmfmac43455-sdio.clm_blob file.
//
//go:embed firmware/brcm/brcmfmac43455-sdio.clm_blob
var FirmwareBrcmBrcmfmac43455SdioClmBlob []byte

// FirmwareBrcmBrcmfmac43455SdioRaspberry returns the firmware/brcm/brcmfmac43455-sdio.Raspberry file.
//
//go:embed firmware/brcm/brcmfmac43455-sdio.Raspberry
var FirmwareBrcmBrcmfmac43455SdioRaspberry []byte

// ConfigTxt is the default configuration for the Raspberry Pi 4.
//
//go:embed config.txt
var ConfigTxt []byte

// Files is the mapping to the embedded iPXE binaries.
var Files = map[string][]byte{
	FirmwareFileName:                         RpiEfi,
	"fixup4.dat":                             Fixup4Dat,
	"start4.elf":                             Start4ElfDat,
	"bcm2711-rpi-4-b.dtb":                    Bcm2711Rpi4BDtb,
	"bcm2711-rpi-400.dtb":                    Bcm2711Rpi400Dtb,
	"bcm2711-rpi-cm4.dtb":                    Bcm2711RpiCm4Dtb,
	"miniuart-bt.dtbo":                       OverlaysMiniUartBtDtbo,
	"upstream-pi4.dtbo":                      OverlaysUpstreamPi4Dtbo,
	"rpi-poe-plus.dtbo":                      OverlaysRpiPoePlusDtbo,
	"act-led.dtbo":                           OverlaysActLedDtbo,
	"adafruit-st7735r.dtbo":                  OverlaysAdafruitSt7735rDtbo,
	"adafruit18.dtbo":                        OverlaysAdafruit18Dtbo,
	"adau1977-adc.dtbo":                      OverlaysAdau1977AdcDtbo,
	"adau7002-simple.dtbo":                   OverlaysAdau7002SimpleDtbo,
	"ads1015.dtbo":                           OverlaysAds1015Dtbo,
	"ads1115.dtbo":                           OverlaysAds1115Dtbo,
	"ads7846.dtbo":                           OverlaysAds7846Dtbo,
	"adv7282m.dtbo":                          OverlaysAdv7282mDtbo,
	"adv728x-m.dtbo":                         OverlaysAdv728xMDtbo,
	"akkordion-iqdacplus.dtbo":               OverlaysAkkordionIqdacplusDtbo,
	"allo-boss-dac-pcm512x-audio.dtbo":       OverlaysAlloBossDacPcm512xAudioDtbo,
	"allo-boss2-dac-audio.dtbo":              OverlaysAlloBoss2DacAudioDtbo,
	"allo-digione.dtbo":                      OverlaysAlloDigioneDtbo,
	"allo-katana-dac-audio.dtbo":             OverlaysAlloKatanaDacAudioDtbo,
	"allo-piano-dac-pcm512x-audio.dtbo":      OverlaysAlloPianoDacPcm512xAudioDtbo,
	"allo-piano-dac-plus-pcm512x-audio.dtbo": OverlaysAlloPianoDacPlusPcm512xAudioDtbo,
	"anyspi.dtbo":                            OverlaysAnyspiDtbo,
	"apds9960.dtbo":                          OverlaysApds9960Dtbo,
	"applepi-dac.dtbo":                       OverlaysApplepiDacDtbo,
	"arducam-64mp.dtbo":                      OverlaysArducam64mpDtbo,
	"arducam-pivariety.dtbo":                 OverlaysArducamPivarietyDtbo,
	"at86rf233.dtbo":                         OverlaysAt86rf233Dtbo,
	"audioinjector-addons.dtbo":              OverlaysAudioinjectorAddonsDtbo,
	"audioinjector-bare-i2s.dtbo":            OverlaysAudioinjectorBareI2sDtbo,
	"audioinjector-isolated-soundcard.dtbo":  OverlaysAudioinjectorIsolatedSoundcardDtbo,
	"audioinjector-ultra.dtbo":               OverlaysAudioinjectorUltraDtbo,
	"audioinjector-wm8731-audio.dtbo":        OverlaysAudioinjectorWm8731AudioDtbo,
	"audiosense-pi.dtbo":                     OverlaysAudiosensePiDtbo,
	"audremap-pi5.dtbo":                      OverlaysAudremapPi5Dtbo,
	"audremap.dtbo":                          OverlaysAudremapDtbo,
	"balena-fin.dtbo":                        OverlaysBalenaFinDtbo,
	"bcm2712d0.dtbo":                         OverlaysBcm2712d0Dtbo,
	"camera-mux-2port.dtbo":                  OverlaysCameraMux2portDtbo,
	"camera-mux-4port.dtbo":                  OverlaysCameraMux4portDtbo,
	"cap1106.dtbo":                           OverlaysCap1106Dtbo,
	"chipcap2.dtbo":                          OverlaysChipcap2Dtbo,
	"chipdip-dac.dtbo":                       OverlaysChipdipDacDtbo,
	"cirrus-wm5102.dtbo":                     OverlaysCirrusWm5102Dtbo,
	"cm-swap-i2c0.dtbo":                      OverlaysCmSwapI2c0Dtbo,
	"cma.dtbo":                               OverlaysCmaDtbo,
	"crystalfontz-cfa050_pi_m.dtbo":          OverlaysCrystalfontzCfa050PiMDtbo,
	"cutiepi-panel.dtbo":                     OverlaysCutiepiPanelDtbo,
	"dacberry400.dtbo":                       OverlaysDacberry400Dtbo,
	"dht11.dtbo":                             OverlaysDht11Dtbo,
	"dionaudio-kiwi.dtbo":                    OverlaysDionaudioKiwiDtbo,
	"dionaudio-loco-v2.dtbo":                 OverlaysDionaudioLocoV2Dtbo,
	"dionaudio-loco.dtbo":                    OverlaysDionaudioLocoDtbo,
	"disable-bt-pi5.dtbo":                    OverlaysDisableBtPi5Dtbo,
	"disable-bt.dtbo":                        OverlaysDisableBtDtbo,
	"disable-emmc2.dtbo":                     OverlaysDisableEmmc2Dtbo,
	"disable-wifi-pi5.dtbo":                  OverlaysDisableWifiPi5Dtbo,
	"disable-wifi.dtbo":                      OverlaysDisableWifiDtbo,
	"dpi18.dtbo":                             OverlaysDpi18Dtbo,
	"dpi18cpadhi.dtbo":                       OverlaysDpi18cpadhiDtbo,
	"dpi24.dtbo":                             OverlaysDpi24Dtbo,
	"draws.dtbo":                             OverlaysDrawsDtbo,
	"dwc-otg-deprecated.dtbo":                OverlaysDwcOtgDeprecatedDtbo,
	"dwc2.dtbo":                              OverlaysDwc2Dtbo,
	"edt-ft5406.dtbo":                        OverlaysEdtFt5406Dtbo,
	"enc28j60-spi2.dtbo":                     OverlaysEnc28j60Spi2Dtbo,
	"enc28j60.dtbo":                          OverlaysEnc28j60Dtbo,
	"exc3000.dtbo":                           OverlaysExc3000Dtbo,
	"ezsound-6x8iso.dtbo":                    OverlaysEzsound6x8isoDtbo,
	"fbtft.dtbo":                             OverlaysFbtftDtbo,
	"fe-pi-audio.dtbo":                       OverlaysFePiAudioDtbo,
	"fsm-demo.dtbo":                          OverlaysFsmDemoDtbo,
	"gc9a01.dtbo":                            OverlaysGc9a01Dtbo,
	"ghost-amp.dtbo":                         OverlaysGhostAmpDtbo,
	"goodix.dtbo":                            OverlaysGoodixDtbo,
	"googlevoicehat-soundcard.dtbo":          OverlaysGooglevoicehatSoundcardDtbo,
	"gpio-charger.dtbo":                      OverlaysGpioChargerDtbo,
	"gpio-fan.dtbo":                          OverlaysGpioFanDtbo,
	"gpio-hog.dtbo":                          OverlaysGpioHogDtbo,
	"gpio-ir-tx.dtbo":                        OverlaysGpioIrTxDtbo,
	"gpio-ir.dtbo":                           OverlaysGpioIrDtbo,
	"gpio-key.dtbo":                          OverlaysGpioKeyDtbo,
	"gpio-led.dtbo":                          OverlaysGpioLedDtbo,
	"gpio-no-bank0-irq.dtbo":                 OverlaysGpioNoBank0IrqDtbo,
	"gpio-no-irq.dtbo":                       OverlaysGpioNoIrqDtbo,
	"gpio-poweroff.dtbo":                     OverlaysGpioPoweroffDtbo,
	"gpio-shutdown.dtbo":                     OverlaysGpioShutdownDtbo,
	"hat_map.dtb":                            OverlaysHatMapDtb,
	"hd44780-i2c-lcd.dtbo":                   OverlaysHd44780I2cLcdDtbo,
	"hd44780-lcd.dtbo":                       OverlaysHd44780LcdDtbo,
	"hdmi-backlight-hwhack-gpio.dtbo":        OverlaysHdmiBacklightHwhackGpioDtbo,
	"hifiberry-adc.dtbo":                     OverlaysHifiberryAdcDtbo,
	"hifiberry-adc8x.dtbo":                   OverlaysHifiberryAdc8xDtbo,
	"hifiberry-amp.dtbo":                     OverlaysHifiberryAmpDtbo,
	"hifiberry-amp100.dtbo":                  OverlaysHifiberryAmp100Dtbo,
	"hifiberry-amp3.dtbo":                    OverlaysHifiberryAmp3Dtbo,
	"hifiberry-amp4pro.dtbo":                 OverlaysHifiberryAmp4proDtbo,
	"hifiberry-dac.dtbo":                     OverlaysHifiberryDacDtbo,
	"hifiberry-dac8x.dtbo":                   OverlaysHifiberryDac8xDtbo,
	"hifiberry-dacplus-pro.dtbo":             OverlaysHifiberryDacplusProDtbo,
	"hifiberry-dacplus-std.dtbo":             OverlaysHifiberryDacplusStdDtbo,
	"hifiberry-dacplus.dtbo":                 OverlaysHifiberryDacplusDtbo,
	"hifiberry-dacplusadc.dtbo":              OverlaysHifiberryDacplusadcDtbo,
	"hifiberry-dacplusadcpro.dtbo":           OverlaysHifiberryDacplusadcproDtbo,
	"hifiberry-dacplusdsp.dtbo":              OverlaysHifiberryDacplusdspDtbo,
	"hifiberry-dacplushd.dtbo":               OverlaysHifiberryDacplushdDtbo,
	"hifiberry-digi-pro.dtbo":                OverlaysHifiberryDigiProDtbo,
	"hifiberry-digi.dtbo":                    OverlaysHifiberryDigiDtbo,
	"highperi.dtbo":                          OverlaysHighperiDtbo,
	"hy28a.dtbo":                             OverlaysHy28aDtbo,
	"hy28b-2017.dtbo":                        OverlaysHy28b2017Dtbo,
	"hy28b.dtbo":                             OverlaysHy28bDtbo,
	"i-sabre-q2m.dtbo":                       OverlaysISabreQ2mDtbo,
	"i2c-bcm2708.dtbo":                       OverlaysI2cBcm2708Dtbo,
	"i2c-fan.dtbo":                           OverlaysI2cFanDtbo,
	"i2c-gpio.dtbo":                          OverlaysI2cGpioDtbo,
	"i2c-mux.dtbo":                           OverlaysI2cMuxDtbo,
	"i2c-pwm-pca9685a.dtbo":                  OverlaysI2cPwmPca9685aDtbo,
	"i2c-rtc-gpio.dtbo":                      OverlaysI2cRtcGpioDtbo,
	"i2c-rtc.dtbo":                           OverlaysI2cRtcDtbo,
	"i2c-sensor.dtbo":                        OverlaysI2cSensorDtbo,
	"i2c0-pi5.dtbo":                          OverlaysI2c0Pi5Dtbo,
	"i2c0.dtbo":                              OverlaysI2c0Dtbo,
	"i2c1-pi5.dtbo":                          OverlaysI2c1Pi5Dtbo,
	"i2c1.dtbo":                              OverlaysI2c1Dtbo,
	"i2c2-pi5.dtbo":                          OverlaysI2c2Pi5Dtbo,
	"i2c3-pi5.dtbo":                          OverlaysI2c3Pi5Dtbo,
	"i2c3.dtbo":                              OverlaysI2c3Dtbo,
	"i2c4.dtbo":                              OverlaysI2c4Dtbo,
	"i2c5.dtbo":                              OverlaysI2c5Dtbo,
	"i2c6.dtbo":                              OverlaysI2c6Dtbo,
	"i2s-dac.dtbo":                           OverlaysI2sDacDtbo,
	"i2s-gpio28-31.dtbo":                     OverlaysI2sGpio2831Dtbo,
	"i2s-master-dac.dtbo":                    OverlaysI2sMasterDacDtbo,
	"ilitek251x.dtbo":                        OverlaysIlitek251xDtbo,
	"imx219.dtbo":                            OverlaysImx219Dtbo,
	"imx258.dtbo":                            OverlaysImx258Dtbo,
	"imx283.dtbo":                            OverlaysImx283Dtbo,
	"imx290.dtbo":                            OverlaysImx290Dtbo,
	"imx296.dtbo":                            OverlaysImx296Dtbo,
	"imx327.dtbo":                            OverlaysImx327Dtbo,
	"imx335.dtbo":                            OverlaysImx335Dtbo,
	"imx378.dtbo":                            OverlaysImx378Dtbo,
	"imx415.dtbo":                            OverlaysImx415Dtbo,
	"imx462.dtbo":                            OverlaysImx462Dtbo,
	"imx477.dtbo":                            OverlaysImx477Dtbo,
	"imx500-pi5.dtbo":                        OverlaysImx500Pi5Dtbo,
	"imx500.dtbo":                            OverlaysImx500Dtbo,
	"imx519.dtbo":                            OverlaysImx519Dtbo,
	"imx708.dtbo":                            OverlaysImx708Dtbo,
	"interludeaudio-analog.dtbo":             OverlaysInterludeaudioAnalogDtbo,
	"interludeaudio-digital.dtbo":            OverlaysInterludeaudioDigitalDtbo,
	"iqaudio-codec.dtbo":                     OverlaysIqaudioCodecDtbo,
	"iqaudio-dac.dtbo":                       OverlaysIqaudioDacDtbo,
	"iqaudio-dacplus.dtbo":                   OverlaysIqaudioDacplusDtbo,
	"iqaudio-digi-wm8804-audio.dtbo":         OverlaysIqaudioDigiWm8804AudioDtbo,
	"iqs550.dtbo":                            OverlaysIqs550Dtbo,
	"irs1125.dtbo":                           OverlaysIrs1125Dtbo,
	"jedec-spi-nor.dtbo":                     OverlaysJedecSpiNorDtbo,
	"justboom-both.dtbo":                     OverlaysJustboomBothDtbo,
	"justboom-dac.dtbo":                      OverlaysJustboomDacDtbo,
	"justboom-digi.dtbo":                     OverlaysJustboomDigiDtbo,
	"ltc294x.dtbo":                           OverlaysLtc294xDtbo,
	"max98357a.dtbo":                         OverlaysMax98357aDtbo,
	"maxtherm.dtbo":                          OverlaysMaxthermDtbo,
	"mbed-dac.dtbo":                          OverlaysMbedDacDtbo,
	"mcp23017.dtbo":                          OverlaysMcp23017Dtbo,
	"mcp23s17.dtbo":                          OverlaysMcp23s17Dtbo,
	"mcp2515-can0.dtbo":                      OverlaysMcp2515Can0Dtbo,
	"mcp2515-can1.dtbo":                      OverlaysMcp2515Can1Dtbo,
	"mcp2515.dtbo":                           OverlaysMcp2515Dtbo,
	"mcp251xfd.dtbo":                         OverlaysMcp251xfdDtbo,
	"mcp3008.dtbo":                           OverlaysMcp3008Dtbo,
	"mcp3202.dtbo":                           OverlaysMcp3202Dtbo,
	"mcp342x.dtbo":                           OverlaysMcp342xDtbo,
	"media-center.dtbo":                      OverlaysMediaCenterDtbo,
	"merus-amp.dtbo":                         OverlaysMerusAmpDtbo,
	"midi-uart0-pi5.dtbo":                    OverlaysMidiUart0Pi5Dtbo,
	"midi-uart0.dtbo":                        OverlaysMidiUart0Dtbo,
	"midi-uart1-pi5.dtbo":                    OverlaysMidiUart1Pi5Dtbo,
	"midi-uart1.dtbo":                        OverlaysMidiUart1Dtbo,
	"midi-uart2-pi5.dtbo":                    OverlaysMidiUart2Pi5Dtbo,
	"midi-uart2.dtbo":                        OverlaysMidiUart2Dtbo,
	"midi-uart3-pi5.dtbo":                    OverlaysMidiUart3Pi5Dtbo,
	"midi-uart3.dtbo":                        OverlaysMidiUart3Dtbo,
	"midi-uart4-pi5.dtbo":                    OverlaysMidiUart4Pi5Dtbo,
	"midi-uart4.dtbo":                        OverlaysMidiUart4Dtbo,
	"midi-uart5.dtbo":                        OverlaysMidiUart5Dtbo,
	"minipitft13.dtbo":                       OverlaysMinipitft13Dtbo,
	"mipi-dbi-spi.dtbo":                      OverlaysMipiDbiSpiDtbo,
	"mira220.dtbo":                           OverlaysMira220Dtbo,
	"mlx90640.dtbo":                          OverlaysMlx90640Dtbo,
	"mmc.dtbo":                               OverlaysMmcDtbo,
	"mz61581.dtbo":                           OverlaysMz61581Dtbo,
	"ov2311.dtbo":                            OverlaysOv2311Dtbo,
	"ov5647.dtbo":                            OverlaysOv5647Dtbo,
	"ov64a40.dtbo":                           OverlaysOv64a40Dtbo,
	"ov7251.dtbo":                            OverlaysOv7251Dtbo,
	"ov9281.dtbo":                            OverlaysOv9281Dtbo,
	"overlay_map.dtb":                        OverlaysOverlayMapDtb,
	"papirus.dtbo":                           OverlaysPapirusDtbo,
	"pca953x.dtbo":                           OverlaysPca953xDtbo,
	"pcf857x.dtbo":                           OverlaysPcf857xDtbo,
	"pcie-32bit-dma-pi5.dtbo":                OverlaysPcie32bitDmaPi5Dtbo,
	"pcie-32bit-dma.dtbo":                    OverlaysPcie32bitDmaDtbo,
	"pciex1-compat-pi5.dtbo":                 OverlaysPciex1CompatPi5Dtbo,
	"pibell.dtbo":                            OverlaysPibellDtbo,
	"pifacedigital.dtbo":                     OverlaysPifacedigitalDtbo,
	"pifi-40.dtbo":                           OverlaysPifi40Dtbo,
	"pifi-dac-hd.dtbo":                       OverlaysPifiDacHdDtbo,
	"pifi-dac-zero.dtbo":                     OverlaysPifiDacZeroDtbo,
	"pifi-mini-210.dtbo":                     OverlaysPifiMini210Dtbo,
	"piglow.dtbo":                            OverlaysPiglowDtbo,
	"pimidi.dtbo":                            OverlaysPimidiDtbo,
	"pineboards-hat-ai.dtbo":                 OverlaysPineboardsHatAiDtbo,
	"pineboards-hatdrive-poe-plus.dtbo":      OverlaysPineboardsHatdrivePoePlusDtbo,
	"piscreen.dtbo":                          OverlaysPiscreenDtbo,
	"piscreen2r.dtbo":                        OverlaysPiscreen2rDtbo,
	"pisound-micro.dtbo":                     OverlaysPisoundMicroDtbo,
	"pisound-pi5.dtbo":                       OverlaysPisoundPi5Dtbo,
	"pisound.dtbo":                           OverlaysPisoundDtbo,
	"pitft22.dtbo":                           OverlaysPitft22Dtbo,
	"pitft28-capacitive.dtbo":                OverlaysPitft28CapacitiveDtbo,
	"pitft28-resistive.dtbo":                 OverlaysPitft28ResistiveDtbo,
	"pitft35-resistive.dtbo":                 OverlaysPitft35ResistiveDtbo,
	"pivision.dtbo":                          OverlaysPivisionDtbo,
	"pps-gpio.dtbo":                          OverlaysPpsGpioDtbo,
	"proto-codec.dtbo":                       OverlaysProtoCodecDtbo,
	"pwm-2chan.dtbo":                         OverlaysPwm2chanDtbo,
	"pwm-gpio-fan.dtbo":                      OverlaysPwmGpioFanDtbo,
	"pwm-gpio.dtbo":                          OverlaysPwmGpioDtbo,
	"pwm-ir-tx.dtbo":                         OverlaysPwmIrTxDtbo,
	"pwm-pio.dtbo":                           OverlaysPwmPioDtbo,
	"pwm.dtbo":                               OverlaysPwmDtbo,
	"pwm1.dtbo":                              OverlaysPwm1Dtbo,
	"qca7000-uart0.dtbo":                     OverlaysQca7000Uart0Dtbo,
	"qca7000.dtbo":                           OverlaysQca7000Dtbo,
	"ramoops-pi4.dtbo":                       OverlaysRamoopsPi4Dtbo,
	"ramoops.dtbo":                           OverlaysRamoopsDtbo,
	"rootmaster.dtbo":                        OverlaysRootmasterDtbo,
	"rotary-encoder.dtbo":                    OverlaysRotaryEncoderDtbo,
	"rpi-backlight.dtbo":                     OverlaysRpiBacklightDtbo,
	"rpi-codeczero.dtbo":                     OverlaysRpiCodeczzeroDtbo,
	"rpi-dacplus.dtbo":                       OverlaysRpiDacplusDtbo,
	"rpi-dacpro.dtbo":                        OverlaysRpiDacproDtbo,
	"rpi-digiampplus.dtbo":                   OverlaysRpiDigiampplusDtbo,
	"rpi-ft5406.dtbo":                        OverlaysRpiFt5406Dtbo,
	"rpi-fw-uart.dtbo":                       OverlaysRpiFwUartDtbo,
	"rpi-poe.dtbo":                           OverlaysRpiPoeDtbo,
	"rpi-sense-v2.dtbo":                      OverlaysRpiSenseV2Dtbo,
	"rpi-sense.dtbo":                         OverlaysRpiSenseDtbo,
	"rpi-tv.dtbo":                            OverlaysRpiTvDtbo,
	"rra-digidac1-wm8741-audio.dtbo":         OverlaysRraDigidac1Wm8741AudioDtbo,
	"sainsmart18.dtbo":                       OverlaysSainsmart18Dtbo,
	"sc16is750-i2c.dtbo":                     OverlaysSc16is750I2cDtbo,
	"sc16is752-i2c.dtbo":                     OverlaysSc16is752I2cDtbo,
	"sc16is75x-spi.dtbo":                     OverlaysSc16is75xSpiDtbo,
	"sdhost.dtbo":                            OverlaysSdhostDtbo,
	"sdio-pi5.dtbo":                          OverlaysSdioPi5Dtbo,
	"sdio.dtbo":                              OverlaysSdioDtbo,
	"seeed-can-fd-hat-v1.dtbo":               OverlaysSeeedCanFdHatV1Dtbo,
	"seeed-can-fd-hat-v2.dtbo":               OverlaysSeeedCanFdHatV2Dtbo,
	"sh1106-spi.dtbo":                        OverlaysSh1106SpiDtbo,
	"si446x-spi0.dtbo":                       OverlaysSi446xSpi0Dtbo,
	"smi-dev.dtbo":                           OverlaysSmiDevDtbo,
	"smi-nand.dtbo":                          OverlaysSmiNandDtbo,
	"smi.dtbo":                               OverlaysSmiDtbo,
	"spi-gpio35-39.dtbo":                     OverlaysSpiGpio3539Dtbo,
	"spi-gpio40-45.dtbo":                     OverlaysSpiGpio4045Dtbo,
	"spi-rtc.dtbo":                           OverlaysSpiRtcDtbo,
	"spi0-0cs.dtbo":                          OverlaysSpi00csDtbo,
	"spi0-1cs-inverted.dtbo":                 OverlaysSpi01csInvertedDtbo,
	"spi0-1cs.dtbo":                          OverlaysSpi01csDtbo,
	"spi0-2cs.dtbo":                          OverlaysSpi02csDtbo,
	"spi1-1cs.dtbo":                          OverlaysSpi11csDtbo,
	"spi1-2cs.dtbo":                          OverlaysSpi12csDtbo,
	"spi1-3cs.dtbo":                          OverlaysSpi13csDtbo,
	"spi2-1cs-pi5.dtbo":                      OverlaysSpi21csPi5Dtbo,
	"spi2-1cs.dtbo":                          OverlaysSpi21csDtbo,
	"spi2-2cs-pi5.dtbo":                      OverlaysSpi22csPi5Dtbo,
	"spi2-2cs.dtbo":                          OverlaysSpi22csDtbo,
	"spi2-3cs.dtbo":                          OverlaysSpi23csDtbo,
	"spi3-1cs-pi5.dtbo":                      OverlaysSpi31csPi5Dtbo,
	"spi3-1cs.dtbo":                          OverlaysSpi31csDtbo,
	"spi3-2cs-pi5.dtbo":                      OverlaysSpi32csPi5Dtbo,
	"spi3-2cs.dtbo":                          OverlaysSpi32csDtbo,
	"spi4-1cs.dtbo":                          OverlaysSpi41csDtbo,
	"spi4-2cs.dtbo":                          OverlaysSpi42csDtbo,
	"spi5-1cs-pi5.dtbo":                      OverlaysSpi51csPi5Dtbo,
	"spi5-1cs.dtbo":                          OverlaysSpi51csDtbo,
	"spi5-2cs-pi5.dtbo":                      OverlaysSpi52csPi5Dtbo,
	"spi5-2cs.dtbo":                          OverlaysSpi52csDtbo,
	"spi6-1cs.dtbo":                          OverlaysSpi61csDtbo,
	"spi6-2cs.dtbo":                          OverlaysSpi62csDtbo,
	"ssd1306-spi.dtbo":                       OverlaysSsd1306SpiDtbo,
	"ssd1306.dtbo":                           OverlaysSsd1306Dtbo,
	"ssd1327-spi.dtbo":                       OverlaysSsd1327SpiDtbo,
	"ssd1331-spi.dtbo":                       OverlaysSsd1331SpiDtbo,
	"ssd1351-spi.dtbo":                       OverlaysSsd1351SpiDtbo,
	"sunfounder-pipower3.dtbo":               OverlaysSunfounderPipower3Dtbo,
	"sunfounder-pironman5.dtbo":              OverlaysSunfounderPironman5Dtbo,
	"superaudioboard.dtbo":                   OverlaysSuperaudioboardDtbo,
	"sx150x.dtbo":                            OverlaysSx150xDtbo,
	"tc358743-audio.dtbo":                    OverlaysTc358743AudioDtbo,
	"tc358743-pi5.dtbo":                      OverlaysTc358743Pi5Dtbo,
	"tc358743.dtbo":                          OverlaysTc358743Dtbo,
	"tinylcd35.dtbo":                         OverlaysTinylcd35Dtbo,
	"tpm-slb9670.dtbo":                       OverlaysTpmSlb9670Dtbo,
	"tpm-slb9673.dtbo":                       OverlaysTpmSlb9673Dtbo,
	"uart0-pi5.dtbo":                         OverlaysUart0Pi5Dtbo,
	"uart0.dtbo":                             OverlaysUart0Dtbo,
	"uart1-pi5.dtbo":                         OverlaysUart1Pi5Dtbo,
	"uart1.dtbo":                             OverlaysUart1Dtbo,
	"uart2-pi5.dtbo":                         OverlaysUart2Pi5Dtbo,
	"uart2.dtbo":                             OverlaysUart2Dtbo,
	"uart3-pi5.dtbo":                         OverlaysUart3Pi5Dtbo,
	"uart3.dtbo":                             OverlaysUart3Dtbo,
	"uart4-pi5.dtbo":                         OverlaysUart4Pi5Dtbo,
	"uart4.dtbo":                             OverlaysUart4Dtbo,
	"uart5.dtbo":                             OverlaysUart5Dtbo,
	"udrc.dtbo":                              OverlaysUdrcDtbo,
	"ugreen-dabboard.dtbo":                   OverlaysUgreenDabboardDtbo,
	"upstream.dtbo":                          OverlaysUpstreamDtbo,
	"vc4-fkms-v3d-pi4.dtbo":                  OverlaysVc4FkmsV3dPi4Dtbo,
	"vc4-fkms-v3d.dtbo":                      OverlaysVc4FkmsV3dDtbo,
	"vc4-kms-dpi-generic.dtbo":               OverlaysVc4KmsDpiGenericDtbo,
	"vc4-kms-dpi-hyperpixel2r.dtbo":          OverlaysVc4KmsDpiHyperpixel2rDtbo,
	"vc4-kms-dpi-hyperpixel4.dtbo":           OverlaysVc4KmsDpiHyperpixel4Dtbo,
	"vc4-kms-dpi-hyperpixel4sq.dtbo":         OverlaysVc4KmsDpiHyperpixel4sqDtbo,
	"vc4-kms-dpi-panel.dtbo":                 OverlaysVc4KmsDpiPanelDtbo,
	"vc4-kms-dsi-7inch.dtbo":                 OverlaysVc4KmsDsi7inchDtbo,
	"vc4-kms-dsi-generic.dtbo":               OverlaysVc4KmsDsiGenericDtbo,
	"vc4-kms-dsi-ili9881-5inch.dtbo":         OverlaysVc4KmsDsiIli98815inchDtbo,
	"vc4-kms-dsi-ili9881-7inch.dtbo":         OverlaysVc4KmsDsiIli98817inchDtbo,
	"vc4-kms-dsi-lt070me05000-v2.dtbo":       OverlaysVc4KmsDsiLt070me05000V2Dtbo,
	"vc4-kms-dsi-lt070me05000.dtbo":          OverlaysVc4KmsDsiLt070me05000Dtbo,
	"vc4-kms-dsi-waveshare-800x480.dtbo":     OverlaysVc4KmsDsiWaveshare800x480Dtbo,
	"vc4-kms-dsi-waveshare-panel-v2.dtbo":    OverlaysVc4KmsDsiWavesharePanelV2Dtbo,
	"vc4-kms-dsi-waveshare-panel.dtbo":       OverlaysVc4KmsDsiWavesharePanelDtbo,
	"vc4-kms-kippah-7inch.dtbo":              OverlaysVc4KmsKippah7inchDtbo,
	"vc4-kms-v3d-pi4.dtbo":                   OverlaysVc4KmsV3dPi4Dtbo,
	"vc4-kms-v3d-pi5.dtbo":                   OverlaysVc4KmsV3dPi5Dtbo,
	"vc4-kms-v3d.dtbo":                       OverlaysVc4KmsV3dDtbo,
	"vc4-kms-vga666.dtbo":                    OverlaysVc4KmsVga666Dtbo,
	"vga666.dtbo":                            OverlaysVga666Dtbo,
	"vl805.dtbo":                             OverlaysVl805Dtbo,
	"w1-gpio-pi5.dtbo":                       OverlaysW1GpioPi5Dtbo,
	"w1-gpio-pullup-pi5.dtbo":                OverlaysW1GpioPullupPi5Dtbo,
	"w1-gpio-pullup.dtbo":                    OverlaysW1GpioPullupDtbo,
	"w1-gpio.dtbo":                           OverlaysW1GpioDtbo,
	"w5500.dtbo":                             OverlaysW5500Dtbo,
	"watterott-display.dtbo":                 OverlaysWatterottDisplayDtbo,
	"waveshare-can-fd-hat-mode-a.dtbo":       OverlaysWaveshareCanFdHatModeADtbo,
	"waveshare-can-fd-hat-mode-b.dtbo":       OverlaysWaveshareCanFdHatModeBDtbo,
	"wifimac.dtbo":                           OverlaysWifimacDtbo,
	"wittypi.dtbo":                           OverlaysWittypiDtbo,
	"wm8960-soundcard.dtbo":                  OverlaysWm8960SoundcardDtbo,
	"ws2812-pio.dtbo":                        OverlaysWs2812PioDtbo,
	"brcmfmac43455-sdio.bin":                 FirmwareBrcmBrcmfmac43455SdioBin,
	"brcmfmac43455-sdio.txt":                 FirmwareBrcmBrcmfmac43455SdioTxt,
	"brcmfmac43455-sdio.clm_blob":            FirmwareBrcmBrcmfmac43455SdioClmBlob,
	"brcmfmac43455-sdio.Raspberry":           FirmwareBrcmBrcmfmac43455SdioRaspberry,
	"config.txt":                             ConfigTxt,
	"cmdline.txt":                            []byte("tink_worker_image=ghcr.io/tinkerbell/tink-agent:latest"),
	"bootcfg.txt":                            []byte("TFTP_PREFIX=2"),
}

func Read(macAddr net.HardwareAddr) ([]byte, error) {
	// Use cached varstore to avoid repeated parsing
	vs, err := varstore.New(RpiEfi)
	if err != nil {
		return nil, err
	}

	vl, err := vs.GetVarList()
	if err != nil {
		return nil, err
	}

	bootOption, err := efi.NewPxeBootOption(macAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create PXE boot option: %v", err)
	}

	if err = vl.Add(bootOption); err != nil {
		return nil, fmt.Errorf("failed to add PXE boot option: %v", err)
	}

	bootNextTemplate := &efi.EfiVar{
		Name: efi.FromString("BootNext"),
		Guid: efi.EFI_GLOBAL_VARIABLE_GUID,
		Attr: efi.EfiVariableDefault | efi.EfiVariableRuntimeAccess,
		Data: []byte{0x99, 0x00},
	}

	if err = vl.Add(bootNextTemplate); err != nil {
		return nil, fmt.Errorf("failed to add BootNext variable: %v", err)
	}

	return vs.ReadAll(vl)
}
