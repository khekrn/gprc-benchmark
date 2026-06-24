//! Transport adapters: the gRPC service (all four RPC styles) and the Axum
//! REST surface. Both translate between the wire types and the application
//! service, and map `UserError` to the right status.

pub mod grpc;
pub mod http;
