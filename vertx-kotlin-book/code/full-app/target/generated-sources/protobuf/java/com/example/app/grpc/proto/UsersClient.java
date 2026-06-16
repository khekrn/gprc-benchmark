package com.example.app.grpc.proto;

import io.vertx.core.Future;
import io.vertx.core.Completable;
import io.vertx.core.Handler;
import io.vertx.core.net.SocketAddress;
import io.vertx.grpc.client.GrpcClient;
import io.vertx.core.streams.ReadStream;
import io.vertx.core.streams.WriteStream;
import io.vertx.grpc.common.GrpcStatus;
import io.vertx.grpc.common.ServiceName;
import io.vertx.grpc.common.ServiceMethod;
import io.vertx.grpc.common.GrpcMessageDecoder;
import io.vertx.grpc.common.GrpcMessageEncoder;

/**
 * <p>A client for invoking the Users gRPC service.</p>
 */
public interface UsersClient extends Users {

  /**
   * Calls the GetUser RPC service method.
   *
   * @param request the com.example.app.grpc.proto.GetUserRequest request message
   * @return a future of the com.example.app.grpc.proto.UserReply response message
   */
  Future<com.example.app.grpc.proto.UserReply> getUser(com.example.app.grpc.proto.GetUserRequest request);

  /**
   * Calls the CreateUser RPC service method.
   *
   * @param request the com.example.app.grpc.proto.CreateUserRequest request message
   * @return a future of the com.example.app.grpc.proto.UserReply response message
   */
  Future<com.example.app.grpc.proto.UserReply> createUser(com.example.app.grpc.proto.CreateUserRequest request);

  /**
   * Calls the ListUsers RPC service method.
   *
   * @param request the com.example.app.grpc.proto.ListUsersRequest request message
   * @return a future of the com.example.app.grpc.proto.UserReply response messages
   */
  Future<ReadStream<com.example.app.grpc.proto.UserReply>> listUsers(com.example.app.grpc.proto.ListUsersRequest request);

  /**
   * Calls the ImportUsers RPC service method.
   *
   * @param completable a completable that will be passed a stream to which the com.example.app.grpc.proto.CreateUserRequest request messages can be written to.
   * @return a future of the com.example.app.grpc.proto.ImportSummary response message
   */
  Future<com.example.app.grpc.proto.ImportSummary> importUsers(Completable<WriteStream<com.example.app.grpc.proto.CreateUserRequest>> completable);

  /**
   * Calls the ImportUsers RPC service method.
   *
   * @param streamOfMessages a stream of messages to be sent to the service
   * @return a future of the com.example.app.grpc.proto.ImportSummary response message
   */
  default Future<com.example.app.grpc.proto.ImportSummary> importUsers(ReadStream<com.example.app.grpc.proto.CreateUserRequest> streamOfMessages) {
    io.vertx.core.streams.Pipe<com.example.app.grpc.proto.CreateUserRequest> pipe = streamOfMessages.pipe();
    return importUsers((result, error) -> {
        if (error == null) {
          pipe.to(result);
        } else {
          pipe.close();
        }
    });
  }

  /**
   * Calls the Chat RPC service method.
   *
   * @param compltable a completable that will be passed a stream to which the com.example.app.grpc.proto.ChatMessage request messages can be written to.
   * @return a future of the com.example.app.grpc.proto.ChatMessage response messages
   */
  Future<ReadStream<com.example.app.grpc.proto.ChatMessage>> chat(Completable<WriteStream<com.example.app.grpc.proto.ChatMessage>> completable);

  /**
   * Calls the Chat RPC service method.
   *
    * @param streamOfMessages a stream of messages to be sent to the service
   * @return a future of the com.example.app.grpc.proto.ChatMessage response messages
   */
  default Future<ReadStream<com.example.app.grpc.proto.ChatMessage>> chat(ReadStream<com.example.app.grpc.proto.ChatMessage> streamOfMessages) {
    io.vertx.core.streams.Pipe<com.example.app.grpc.proto.ChatMessage> pipe = streamOfMessages.pipe();
    return chat((result, error) -> {
        if (error == null) {
          pipe.to(result);
        } else {
          pipe.close();
        }
    });
  }
}
