use std::error::Error;
use std::path::PathBuf;

fn main() -> Result<(), Box<dyn Error>> {
    let out_dir = PathBuf::from(std::env::var("OUT_DIR")?);

    tonic_prost_build::configure()
        .file_descriptor_set_path(out_dir.join("calculator_descriptor.bin"))
        .compile_protos(&["proto/calculator.proto"], &["proto"])?;

    Ok(())
}
