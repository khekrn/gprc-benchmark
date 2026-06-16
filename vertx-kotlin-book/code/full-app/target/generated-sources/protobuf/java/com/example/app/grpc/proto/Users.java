package com.example.app.grpc.proto;

import io.vertx.core.Future;
import io.vertx.core.Promise;
import io.vertx.core.Handler;
import io.vertx.core.http.HttpMethod;
import io.vertx.core.streams.ReadStream;
import io.vertx.core.streams.WriteStream;
import io.vertx.grpc.common.GrpcStatus;
import io.vertx.grpc.common.ServiceName;
import io.vertx.grpc.common.ServiceMethod;
import io.vertx.grpc.common.GrpcMessageDecoder;
import io.vertx.grpc.common.GrpcMessageEncoder;
import io.vertx.grpc.server.GrpcServerRequest;
import io.vertx.grpc.server.GrpcServer;
import io.vertx.grpc.server.Service;
import io.vertx.grpc.server.ServiceBuilder;

import com.google.protobuf.Descriptors;

import java.util.LinkedList;
import java.util.ArrayList;
import java.util.List;

/**
 * <p>Contract definition Users service.</p>
 */
public interface Users {

  Future<com.example.app.grpc.proto.UserReply> getUser(com.example.app.grpc.proto.GetUserRequest request);

  Future<com.example.app.grpc.proto.UserReply> createUser(com.example.app.grpc.proto.CreateUserRequest request);

  Future<ReadStream<com.example.app.grpc.proto.UserReply>> listUsers(com.example.app.grpc.proto.ListUsersRequest request);

  Future<com.example.app.grpc.proto.ImportSummary> importUsers(ReadStream<com.example.app.grpc.proto.CreateUserRequest> request);

  Future<ReadStream<com.example.app.grpc.proto.ChatMessage>> chat(ReadStream<com.example.app.grpc.proto.ChatMessage> request);

}
