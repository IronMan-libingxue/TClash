use anyhow::{Result, bail, ensure};
use sha2::{Digest as _, Sha256};

const BACKUP_MAGIC: &[u8] = b"TCLASH\0\x01B";
const PROFILE_MAGIC: &[u8] = b"TCLASH\0\x01P";
const HASH_LENGTH: usize = 32;

pub fn pack_backup(zip: &[u8]) -> Vec<u8> {
    pack(BACKUP_MAGIC, zip)
}

pub fn pack_profile(data: &[u8]) -> Vec<u8> {
    pack(PROFILE_MAGIC, data)
}

fn pack(magic: &[u8], data: &[u8]) -> Vec<u8> {
    let mut result = Vec::with_capacity(magic.len() + HASH_LENGTH + data.len());
    result.extend_from_slice(magic);
    result.extend_from_slice(&Sha256::digest(data));
    result.extend(data.iter().map(|byte| byte ^ 0xa5));
    result
}

pub fn unpack_backup(data: &[u8]) -> Result<Vec<u8>> {
    if data.starts_with(BACKUP_MAGIC) {
        return unpack(BACKUP_MAGIC, data);
    }
    if data.starts_with(b"PK\x03\x04") || data.starts_with(b"PK\x05\x06") {
        return Ok(data.to_vec());
    }
    bail!("Unsupported backup format")
}

pub fn unpack_profile(data: &[u8]) -> Result<Vec<u8>> {
    unpack(PROFILE_MAGIC, data)
}

fn unpack(magic: &[u8], data: &[u8]) -> Result<Vec<u8>> {
    let start = magic.len() + HASH_LENGTH;
    ensure!(data.starts_with(magic) && data.len() >= start, "Unsupported or incomplete TClash file");
    let decoded: Vec<u8> = data[start..].iter().map(|byte| byte ^ 0xa5).collect();
    ensure!(Sha256::digest(&decoded).as_slice() == &data[magic.len()..start], "TClash file checksum mismatch");
    Ok(decoded)
}

#[cfg(test)]
mod tests {
    use super::{pack_backup, unpack_backup};
    use std::io::{Cursor, Write as _};

    #[test]
    fn backup_is_checked_and_cannot_be_used_as_a_zip_without_decoding() {
        let mut writer = zip::ZipWriter::new(Cursor::new(Vec::new()));
        writer.start_file("config.yaml", zip::write::SimpleFileOptions::default()).unwrap();
        writer.write_all(b"mode: rule\n").unwrap();
        let original = writer.finish().unwrap().into_inner();
        let mut wrapped = pack_backup(&original);
        assert!(zip::ZipArchive::new(Cursor::new(&wrapped)).is_err());
        assert_eq!(unpack_backup(&wrapped).unwrap(), original);
        assert_eq!(unpack_backup(&original).unwrap(), original);
        let end = wrapped.len() - 1;
        wrapped[end] ^= 1;
        assert!(unpack_backup(&wrapped).is_err());
    }
}
