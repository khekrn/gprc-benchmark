// Generate Rust gRPC stubs (prost messages + tonic server) from the shared
// proto contract at build time. Output lands in OUT_DIR and is `include!`d
// from src/pb.rs. A file descriptor set is also emitted so the server can
// expose gRPC reflection (grpcurl without the .proto).
fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Point prost-build at the vendored protoc so no system install is needed.
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);

    let out_dir = std::path::PathBuf::from(std::env::var("OUT_DIR")?);
    let descriptor_path = out_dir.join("users_descriptor.bin");

    tonic_build::configure()
        .build_server(true)
        .build_client(false)
        .file_descriptor_set_path(&descriptor_path)
        .compile_protos(&["proto/usersv1/users.proto"], &["proto"])?;

    println!("cargo:rerun-if-changed=proto/usersv1/users.proto");
    Ok(())
}
