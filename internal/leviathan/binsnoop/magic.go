package binsnoop

// AllSignatures returns every supported signature definition.
// This is the equivalent of magic::patterns() in the Rust binwalk codebase.
func AllSignatures() []Signature {
	return []Signature{
		// ===================== Archives =====================
		{Name: "gzip", Magic: gzipMagic(), Parser: gzipParser, Description: gzipDescription, Extractor: extGzip()},
		{Name: "deb", Magic: debMagic(), Parser: debParser, Description: debDescription},
		{Name: "7zip", Magic: sevenzipMagic(), Parser: sevenzipParser, Description: sevenzipDescription, Extractor: extSevenzip()},
		{Name: "xz", Magic: XZMagic(), Parser: XZParser, Description: XZDescription, Extractor: extLzma()},
		{Name: "tarball", Magic: tarballMagic(), Parser: tarballParser, Description: tarballDescription, Extractor: extTar()},
		{Name: "zip", Magic: zipMagic(), Parser: zipParser, Description: zipDescription, Extractor: extSevenzip()},
		{Name: "cpio", Magic: cpioMagic(), Parser: cpioParser, Description: cpioDescription, Extractor: extSevenzip()},
		{Name: "iso9660", Magic: iso9660Magic(), Parser: iso9660Parser, Description: iso9660Description, Extractor: extSevenzip()},
		{Name: "rar", Magic: rarMagic(), Parser: rarParser, Description: rarDescription, Extractor: extRar()},
		{Name: "cab", Magic: cabMagic(), Parser: cabParser, Description: cabDescription, Extractor: extCab()},
		{Name: "arj", Magic: arjMagic(), Parser: arjParser, Description: arjDescription, Extractor: extSevenzip()},
		{Name: "dahua_zip", Magic: dahuaZipMagic(), Parser: dahuaZipParser, Description: dahuaZipDescription, Extractor: extSevenzip()},

		// ===================== Compression =====================
		{Name: "lzma", Magic: LZMAMagic(), Parser: LZMAParser, Description: LZMADescription, Extractor: extLzma()},
		{Name: "bzip2", Magic: Bzip2Magic(), Parser: Bzip2Parser, Description: Bzip2Description, Extractor: extBzip2()},
		{Name: "zstd", Magic: ZSTDMagic(), Parser: ZSTDParser, Description: ZSTDDescription, Extractor: extZstd()},
		{Name: "lz4", Magic: LZ4Magic(), Parser: LZ4Parser, Description: LZ4Description, Extractor: extLz4()},
		{Name: "lzop", Magic: LZOPMagic(), Parser: LZOPParser, Description: LZOPDescription, Extractor: extLzop()},
		{Name: "lzfse", Magic: LZFSEMagic(), Parser: LZFSEParser, Description: LZFSEDescription, Extractor: extLzfse()},
		{Name: "zlib", Short: true, Magic: ZlibMagic(), Parser: ZlibParser, Description: ZlibDescription, Extractor: extZlib()},
		{Name: "compressd", Short: true, Magic: CompressdMagic(), Parser: CompressdParser, Description: CompressdDescription, Extractor: extSevenzip()},

		// ===================== Filesystems =====================
		{Name: "squashfs", Magic: squashfs_magic(), Parser: squashfs_parser, Description: SquashFSDescription, Extractor: extSquashfs()},
		{Name: "cramfs", Magic: cramfs_magic(), Parser: cramfs_parser, Description: CramFSDescription, Extractor: extSevenzip()},
		{Name: "jffs2", Magic: jffs2_magic(), Parser: jffs2_parser, Description: JFFS2Description, Extractor: extJffs2()},
		{Name: "yaffs", Magic: yaffs_magic(), Parser: yaffs_parser, Description: YAFFSDescription, Extractor: extYaffs2()},
		{Name: "romfs", Magic: romfs_magic(), Parser: romfs_parser, Description: RomFSDescription, Extractor: extRomfs()},
		{Name: "ext", Magic: ext_magic(), Parser: ext_parser, Description: EXTDescription, Extractor: extTsk()},
		{Name: "fat", Short: true, MagicOffset: FATMagicOffset, Magic: fat_magic(), Parser: fat_parser, Description: FATDescription, Extractor: extTsk()},
		{Name: "ntfs", Magic: ntfs_magic(), Parser: ntfs_parser, Description: NTFSDescription, Extractor: extTsk()},
		{Name: "apfs", Magic: apfs_magic(), Parser: apfs_parser, Description: APFSDescription, Extractor: extSevenzip()},
		{Name: "btrfs", AlwaysDisplay: true, Magic: btrfs_magic(), Parser: btrfs_parser, Description: BTRFSDescription},
		{Name: "logfs", Magic: logfs_magic(), Parser: logfs_parser, Description: LogFSDescription},
		{Name: "ubi", Magic: ubi_magic(), Parser: ubi_parser, Description: UBIImageDescription, Extractor: extUbi()},
		{Name: "ubifs", Magic: ubifs_magic(), Parser: ubifs_parser, Description: UBIFSDescription, Extractor: extUbifs()},

		// ===================== Images & Media =====================
		{Name: "png", Magic: PNGMagic(), Parser: PNGParser, Description: PNGDescription, Extractor: extPng()},
		{Name: "jpeg", Magic: JPEGMagic(), Parser: JPEGParser, Description: JPEGDescription, Extractor: extJpeg()},
		{Name: "bmp", Magic: BMPMagic(), Parser: BMPParser, Description: BMPDescription, Extractor: extBmp()},
		{Name: "gif", Magic: GIFMagic(), Parser: GIFParser, Description: GIFDescription, Extractor: extGif()},
		{Name: "svg", Magic: SVGMagic(), Parser: SVGParser, Description: SVGDescription, Extractor: extSvg()},
		{Name: "riff", Magic: RIFFMagic(), Parser: RIFFParser, Description: RIFFDescription, Extractor: extRiff()},
		{Name: "pdf", Magic: PDFMagic(), Parser: PDFParser, Description: PDFDescription},

		// ===================== Executables =====================
		{Name: "elf", Magic: ELFMagic(), Parser: ELFParser, Description: ELFDescription},
		{Name: "pe", Magic: PEMagic(), Parser: PEParser, Description: PEDescription},

		// ===================== Firmware Headers =====================
		{Name: "uimage", Magic: uimageMagic(), Parser: uimageParser, Description: uimageDescription, Extractor: extUimage()},
		{Name: "packimg", Magic: packimgMagic(), Parser: packimgParser, Description: packimgDescription},
		{Name: "uboot", AlwaysDisplay: true, Magic: ubootMagic(), Parser: ubootParser, Description: ubootDescription},
		{Name: "trx", Magic: trxMagic(), Parser: trxParser, Description: trxDescription, Extractor: extTrx()},
		{Name: "tplink", Magic: tplinkMagic(), Parser: tplinkParser, Description: tplinkDescription},
		{Name: "tplink_rtos", Magic: tplinkRTOSMagic(), Parser: tplinkRTOSParser, Description: tplinkRTOSDescription},
		{Name: "dlob", Magic: dlobMagic(), Parser: dlobParser, Description: dlobDescription},
		{Name: "arcadyan", Magic: arcadyanMagic(), Parser: arcadyanParser, Description: arcadyanDescription},
		{Name: "chk", Magic: chkMagic(), Parser: chkParser, Description: chkDescription},
		{Name: "seama", AlwaysDisplay: true, Magic: seamaMagic(), Parser: seamaParser, Description: seamaDescription},
		{Name: "cfe", AlwaysDisplay: true, Magic: cfeMagic(), Parser: cfeParser, Description: cfeDescription},
		{Name: "jboot_arm", Magic: jbootARMMagic(), Parser: jbootARMParser, Description: jbootARMDescription},
		{Name: "jboot_stag", Magic: jbootSTAGMagic(), Parser: jbootSTAGParser, Description: jbootSTAGDescription},
		{Name: "jboot_sch2", Magic: jbootSCH2Magic(), Parser: jbootSCH2Parser, Description: jbootSCH2Description, Extractor: extJboot()},
		{Name: "binhdr", Magic: binhdrMagic(), Parser: binhdrParser, Description: binhdrDescription},
		{Name: "autel", Magic: autelMagic(), Parser: autelParser, Description: autelDescription, Extractor: extAutel()},
		{Name: "android_sparse", Magic: androidSparseMagic(), Parser: androidSparseParser, Description: androidSparseDescription, Extractor: extAndroidSparse()},
		{Name: "dtb", Magic: dtbMagic(), Parser: dtbParser, Description: dtbDescription, Extractor: extDtb()},
		{Name: "android_bootimg", Magic: androidBootimgMagic(), Parser: androidBootimgParser, Description: androidBootimgDescription},
		{Name: "dlink_tlv", Magic: dlinkTLVMagic(), Parser: dlinkTLVParser, Description: dlinkTLVDescription, Extractor: extEncfw()},
		{Name: "dlke", Magic: dlkeMagic(), Parser: dlkeParser, Description: dlkeDescription, Extractor: extEncfw()},
		{Name: "shrs", Magic: shrsMagic(), Parser: shrsParser, Description: shrsDescription, Extractor: extEncfw()},
		{Name: "dkbs", Magic: dkbsMagic(), Parser: dkbsParser, Description: dkbsDescription, Extractor: extEncfw()},
		{Name: "dms", Magic: dmsMagic(), Parser: dmsParser, Description: dmsDescription},
		{Name: "encfw", Short: true, AlwaysDisplay: true, Magic: encfwMagic(), Parser: encfwParser, Description: encfwDescription, Extractor: extEncfw()},
		{Name: "encrpted_img", Short: true, Magic: encrptedImgMagic(), Parser: encrptedImgParser, Description: encrptedImgDescription, Extractor: extEncfw()},
		{Name: "wince", Magic: winceMagic(), Parser: winceParser, Description: winceDescription},
		{Name: "mh01", Magic: mh01Magic(), Parser: mh01Parser, Description: mh01Description},
		{Name: "csman", Short: true, Magic: csmanMagic(), Parser: csmanParser, Description: csmanDescription},
		{Name: "dxbc", Magic: dxbcMagic(), Parser: dxbcParser, Description: dxbcDescription},
		{Name: "matter_ota", Short: true, Magic: matterOTAMagic(), Parser: matterOTAParser, Description: matterOTADescription},
		{Name: "rtk", Short: true, Magic: rtkMagic(), Parser: rtkParser, Description: rtkDescription},
		{Name: "dpapi", Short: true, AlwaysDisplay: true, Magic: dpapiMagic(), Parser: dpapiParser, Description: dpapiDescription},
		{Name: "qcow", Short: true, AlwaysDisplay: true, Magic: qcowMagic(), Parser: qcowParser, Description: qcowDescription},

		// ===================== Crypto & Hashes =====================
		{Name: "aes_sbox", Magic: aesSBoxMagic(), Parser: aesSBoxParser, Description: DescriptionAESSBox},
		{Name: "aes_forward_table", Magic: aesForwardTableMagic(), Parser: aesForwardTableParser, Description: DescriptionAESFT},
		{Name: "aes_reverse_table", Magic: aesReverseTableMagic(), Parser: aesReverseTableParser, Description: DescriptionAESRT},
		{Name: "aes_rcon", Magic: aesRconMagic(), Parser: aesRconParser, Description: DescriptionAESRCON},
		{Name: "aes_acceleration_table", Magic: aesAccelerationTableMagic(), Parser: aesAccelerationTableParser, Description: DescriptionAESAcc},
		{Name: "rsa", AlwaysDisplay: true, Magic: rsaMagic(), Parser: rsaParser, Description: DescriptionRSA},
		{Name: "openssl", AlwaysDisplay: true, Magic: opensslMagic(), Parser: opensslParser, Description: DescriptionOpenSSL, Extractor: extEncfw()},
		{Name: "gpg_signed", Short: true, Magic: gpgSignedMagic(), Parser: gpgSignedParser, Description: DescriptionGPGSigned, Extractor: extGpg()},
		{Name: "pem_certificate", Magic: pemCertificateMagic(), Parser: pemParser, Description: DescriptionPEMCertificate, Extractor: extPemCert()},
		{Name: "pem_public_key", AlwaysDisplay: true, Magic: pemPublicKeyMagic(), Parser: pemParser, Description: DescriptionPEMPublicKey, Extractor: extPemKey()},
		{Name: "pem_private_key", AlwaysDisplay: true, Magic: pemPrivateKeyMagic(), Parser: pemParser, Description: DescriptionPEMPrivateKey, Extractor: extPemKey()},
		{Name: "pkcs_der_hash", Magic: derHashMagic(), Parser: derHashParser, Description: DescriptionDERHash},
		{Name: "luks", Magic: luksMagic(), Parser: luksParser, Description: DescriptionLUKS},
		{Name: "crc32", Magic: crc32ConstMagic(), Parser: crc32Parser, Description: DescriptionCRC32},
		{Name: "sha256", Magic: sha256ConstMagic(), Parser: sha256Parser, Description: DescriptionSHA256},
		{Name: "md5", Magic: md5ConstMagic(), Parser: md5Parser, Description: DescriptionMD5},

		// ===================== Misc =====================
		{Name: "copyright", Magic: copyrightMagic(), Parser: copyrightParser, Description: DescriptionCopyright},
		{Name: "wind_kernel", AlwaysDisplay: true, Magic: windKernelMagic(), Parser: windKernelParser, Description: DescriptionWINDKernelVersion},
		{Name: "vxworks_symtab", AlwaysDisplay: true, Magic: vxworksSymtabMagic(), Parser: vxworksSymtabParser, Description: DescriptionVxWorksSymTab},
		{Name: "ecos", AlwaysDisplay: true, Magic: ecosExceptionHandlerMagic(), Parser: ecosExceptionHandlerParser, Description: DescriptionECosExceptionHandler},
		{Name: "linux_kernel", AlwaysDisplay: true, Magic: linuxKernelVersionMagic(), Parser: linuxKernelVersionParser, Description: DescriptionLinuxKernelVersion, Extractor: extLinuxKernel()},
		{Name: "linux_boot_image", Magic: linuxBootImageMagic(), Parser: linuxBootImageParser, Description: DescriptionLinuxBootImage},
		{Name: "linux_arm_zimage", Magic: linuxARMzImageMagic(), Parser: linuxARMzImageParser, Description: DescriptionLinuxARMzImage},
		{Name: "linux_arm64_boot_image", Magic: linuxARM64BootImageMagic(), Parser: linuxARM64BootImageParser, Description: DescriptionLinuxARM64BootImage},
		{Name: "efigpt", Magic: efigptMagic(), Parser: efigptParser, Description: DescriptionEFIGPT, Extractor: extSevenzip()},
		{Name: "mbr", Short: true, MagicOffset: MBRMagicOffset, AlwaysDisplay: true, Magic: mbrMagic(), Parser: mbrParser, Description: DescriptionMBR, Extractor: extMbr()},
		{Name: "uefi_pi_volume", Magic: uefiVolumeMagic(), Parser: uefiVolumeParser, Description: DescriptionUEFIVolume, Extractor: extUefi()},
		{Name: "uefi_capsule", Magic: uefiCapsuleMagic(), Parser: uefiCapsuleParser, Description: DescriptionUEFICapsule, Extractor: extUefi()},
		{Name: "pchrom", Magic: pchRomMagic(), Parser: pchRomParser, Description: DescriptionPCHRom, Extractor: extUefi()},
		{Name: "pcapng", Magic: pcapngMagic(), Parser: pcapngParser, Description: DescriptionPcapNG, Extractor: extPcap()},
		{Name: "pjl", Magic: pjlMagic(), Parser: pjlParser, Description: DescriptionPJL},
		{Name: "srecord", Magic: srecordMagic(), Parser: srecordParser, Description: DescriptionSRecord, Extractor: extSrec()},
		{Name: "srecord_generic", Short: true, Magic: srecordGenericMagic(), Parser: srecordParser, Description: DescriptionSRecordGeneric, Extractor: extSrec()},
		{Name: "dmg", Magic: dmgMagic(), Parser: dmgParser, Description: DescriptionDMG, Extractor: extDmg()},
		{Name: "qnx_ifs", Magic: qnxIFSMagic(), Parser: qnxIFSParser, Description: DescriptionQNXIFS, Extractor: extDumpifs()},
	}
}

// ============================================================================
// External extractor definitions (command-line tools)
// ============================================================================

func extSevenzip() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "7z", Extension: "bin", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0, 2}}
}
func extGzip() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "gzip", Extension: "gz", Arguments: []string{"-d", "-k", "-f", "%e"}, ExitCodes: []int{0}}
}
func extLzma() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "7z", Extension: "xz", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0, 2}}
}
func extTar() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "tar", Extension: "tar", Arguments: []string{"xf", "%e"}, ExitCodes: []int{0}}
}
func extBzip2() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "bzip2", Extension: "bz2", Arguments: []string{"-d", "-k", "-f", "%e"}, ExitCodes: []int{0}}
}
func extZstd() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "zstd", Extension: "zst", Arguments: []string{"-d", "-f", "%e"}, ExitCodes: []int{0}}
}
func extLz4() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "lz4", Extension: "lz4", Arguments: []string{"-d", "-f", "%e", "decompressed.bin"}, ExitCodes: []int{0}}
}
func extLzop() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "lzop", Extension: "lzo", Arguments: []string{"-d", "-f", "%e"}, ExitCodes: []int{0}}
}
func extLzfse() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "lzfse", Extension: "lzfse", Arguments: []string{"-decode", "-i", "%e", "-o", "decompressed.bin"}, ExitCodes: []int{0}}
}
func extZlib() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "7z", Extension: "bin", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0, 2}}
}
func extSquashfs() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "unsquashfs", Extension: "sqsh", Arguments: []string{"-f", "-d", "squashfs-root", "%e"}, ExitCodes: []int{0}}
}
func extJffs2() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "jefferson", Extension: "jffs2", Arguments: []string{"-d", "jffs2-root", "%e"}, ExitCodes: []int{0}}
}
func extYaffs2() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "unyaffs", Extension: "yaffs", Arguments: []string{"%e", "yaffs-root"}, ExitCodes: []int{0}}
}
func extRomfs() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "7z", Extension: "romfs", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0, 2}}
}
func extTsk() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "tsk_recover", Extension: "img", Arguments: []string{"-a", "-e", "%e", "tsk-root"}, ExitCodes: []int{0}}
}
func extCab() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "cabextract", Extension: "cab", Arguments: []string{"%e"}, ExitCodes: []int{0}}
}
func extRar() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "unrar", Extension: "rar", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0}}
}
func extUbi() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "ubireader_extract_images", Extension: "ubi", Arguments: []string{"%e"}, ExitCodes: []int{0}}
}
func extUbifs() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "ubireader_extract_files", Extension: "ubifs", Arguments: []string{"%e"}, ExitCodes: []int{0}}
}
func extPng() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "cp", Extension: "png", Arguments: []string{"%e", "image.png"}, ExitCodes: []int{0}}
}
func extJpeg() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "cp", Extension: "jpg", Arguments: []string{"%e", "image.jpg"}, ExitCodes: []int{0}}
}
func extBmp() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "cp", Extension: "bmp", Arguments: []string{"%e", "image.bmp"}, ExitCodes: []int{0}}
}
func extGif() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "cp", Extension: "gif", Arguments: []string{"%e", "image.gif"}, ExitCodes: []int{0}}
}
func extSvg() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "cp", Extension: "svg", Arguments: []string{"%e", "image.svg"}, ExitCodes: []int{0}}
}
func extRiff() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "cp", Extension: "riff", Arguments: []string{"%e", "media.riff"}, ExitCodes: []int{0}}
}
func extUimage() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "7z", Extension: "bin", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0, 2}}
}
func extTrx() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "7z", Extension: "bin", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0, 2}}
}
func extJboot() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "7z", Extension: "bin", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0, 2}}
}
func extAutel() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "7z", Extension: "bin", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0, 2}}
}
func extAndroidSparse() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "simg2img", Extension: "simg", Arguments: []string{"%e", "output.img"}, ExitCodes: []int{0}}
}
func extDtb() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "dtc", Extension: "dtb", Arguments: []string{"-I", "dtb", "-O", "dts", "-o", "device_tree.dts", "%e"}, ExitCodes: []int{0}}
}
func extEncfw() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "cp", Extension: "bin", Arguments: []string{"%e", "encrypted_firmware.bin"}, ExitCodes: []int{0}}
}
func extGpg() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "gpg", Extension: "gpg", Arguments: []string{"--decrypt", "-o", "decrypted.bin", "%e"}, ExitCodes: []int{0}}
}
func extPemCert() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "cp", Extension: "pem", Arguments: []string{"%e", "certificate.pem"}, ExitCodes: []int{0}}
}
func extPemKey() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "cp", Extension: "pem", Arguments: []string{"%e", "key.pem"}, ExitCodes: []int{0}}
}
func extLinuxKernel() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "7z", Extension: "bin", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0, 2}}
}
func extMbr() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "7z", Extension: "bin", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0, 2}}
}
func extUefi() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "uefi-firmware-parser", Extension: "bin", Arguments: []string{"-e", "-O", "%e"}, ExitCodes: []int{0}}
}
func extPcap() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "cp", Extension: "pcapng", Arguments: []string{"%e", "capture.pcapng"}, ExitCodes: []int{0}}
}
func extSrec() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "srec_cat", Extension: "srec", Arguments: []string{"%e", "-o", "output.bin", "-binary"}, ExitCodes: []int{0}}
}
func extDmg() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "7z", Extension: "dmg", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0, 2}}
}
func extDumpifs() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "dumpifs", Extension: "ifs", Arguments: []string{"-x", "%e"}, ExitCodes: []int{0}}
}
func extIso9660() *Extractor {
	return &Extractor{Type: ExtractorTypeExternal, Command: "7z", Extension: "iso", Arguments: []string{"x", "-y", "%e"}, ExitCodes: []int{0, 2}}
}
