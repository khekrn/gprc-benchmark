// Generate Rust gRPC stubs from the shared proto contract at build time.
// Output lands in OUT_DIR and is `include!`d from src/main.rs.
fn main() -> Result<(), Box<dyn std::error::Error>> {
    tonic_build::configure()
        .build_server(true)
        .build_client(false)
        .compile_protos(&["../proto/command.proto"], &["../proto"])?;
    Ok(())
}
