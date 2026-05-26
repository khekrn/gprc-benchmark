use std::error::Error;

use proto::calculator_server::{Calculator, CalculatorServer};
use tonic::transport::Server;
use tonic_reflection::server::Builder;

mod proto {
    tonic::include_proto!("calculator");

    // Add this line to include the generated file descriptor set
    pub const FILE_DESCRIPTOR_SET: &[u8] =
        tonic::include_file_descriptor_set!("calculator_descriptor");
}

#[derive(Debug, Default)]
struct CalculatorService {}

#[tonic::async_trait]
impl Calculator for CalculatorService {
    async fn add(
        &self,
        request: tonic::Request<proto::CalculationRequest>,
    ) -> Result<tonic::Response<proto::CalculationResponse>, tonic::Status> {
        println!("Received request : {:?}", request);

        let input = request.get_ref();
        let response = proto::CalculationResponse {
            result: input.a + input.b,
        };
        Ok(tonic::Response::new(response))
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    println!("Starting rpc server.....\n");
    let addr = "[::]:50051".parse()?;

    let reflection_service = Builder::configure()
        .register_encoded_file_descriptor_set(proto::FILE_DESCRIPTOR_SET)
        .build_v1()
        .unwrap();

    let calc_service = CalculatorService::default();

    Server::builder()
        .add_service(reflection_service)
        .add_service(CalculatorServer::new(calc_service))
        .serve(addr)
        .await?;
    Ok(())
}
