//! gRPC stubs generated from `proto/command.proto` by `build.rs` (tonic-build).
//!
//! Kept in its own module so the rest of the crate imports tidy paths like
//! `crate::proto::bench_v1::CommandRequest` instead of pulling generated code
//! into `main`. The wire types here are byte-compatible with every other
//! stack — they all compile the same shared proto.

pub mod bench_v1 {
    tonic::include_proto!("bench.v1");
}
