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
public interface UsersGrpcClient extends UsersClient {

  /**
   * GetUser protobuf RPC client service method.
   */
  ServiceMethod<com.example.app.grpc.proto.UserReply, com.example.app.grpc.proto.GetUserRequest> GetUser = ServiceMethod.client(
    ServiceName.create("com.example.app.grpc", "Users"),
    "GetUser",
    GrpcMessageEncoder.encoder(),
    GrpcMessageDecoder.decoder(com.example.app.grpc.proto.UserReply.newBuilder()));

  /**
   * CreateUser protobuf RPC client service method.
   */
  ServiceMethod<com.example.app.grpc.proto.UserReply, com.example.app.grpc.proto.CreateUserRequest> CreateUser = ServiceMethod.client(
    ServiceName.create("com.example.app.grpc", "Users"),
    "CreateUser",
    GrpcMessageEncoder.encoder(),
    GrpcMessageDecoder.decoder(com.example.app.grpc.proto.UserReply.newBuilder()));

  /**
   * ListUsers protobuf RPC client service method.
   */
  ServiceMethod<com.example.app.grpc.proto.UserReply, com.example.app.grpc.proto.ListUsersRequest> ListUsers = ServiceMethod.client(
    ServiceName.create("com.example.app.grpc", "Users"),
    "ListUsers",
    GrpcMessageEncoder.encoder(),
    GrpcMessageDecoder.decoder(com.example.app.grpc.proto.UserReply.newBuilder()));

  /**
   * ImportUsers protobuf RPC client service method.
   */
  ServiceMethod<com.example.app.grpc.proto.ImportSummary, com.example.app.grpc.proto.CreateUserRequest> ImportUsers = ServiceMethod.client(
    ServiceName.create("com.example.app.grpc", "Users"),
    "ImportUsers",
    GrpcMessageEncoder.encoder(),
    GrpcMessageDecoder.decoder(com.example.app.grpc.proto.ImportSummary.newBuilder()));

  /**
   * Chat protobuf RPC client service method.
   */
  ServiceMethod<com.example.app.grpc.proto.ChatMessage, com.example.app.grpc.proto.ChatMessage> Chat = ServiceMethod.client(
    ServiceName.create("com.example.app.grpc", "Users"),
    "Chat",
    GrpcMessageEncoder.encoder(),
    GrpcMessageDecoder.decoder(com.example.app.grpc.proto.ChatMessage.newBuilder()));

  /**
   * Create and return a Users gRPC service client. The assumed wire format is Protobuf.
   *
   * @param client the gRPC client
   * @param host   the host providing the service
   * @return the configured client
   */
  static UsersGrpcClient create(GrpcClient client, SocketAddress host) {
    return new UsersGrpcClientImpl(client, host);
  }

  /**
   * Create and return a Users gRPC service client.
   *
   * @param client     the gRPC client
   * @param host       the host providing the service
   * @param wireFormat the wire format
   * @return the configured client
   */
  static UsersGrpcClient create(GrpcClient client, SocketAddress host, io.vertx.grpc.common.WireFormat wireFormat) {
    return new UsersGrpcClientImpl(client, host, wireFormat);
  }
}

/**
 * The proxy implementation.
 */
class UsersGrpcClientImpl implements UsersGrpcClient {

  private final GrpcClient client;
  private final SocketAddress socketAddress;
  private final io.vertx.grpc.common.WireFormat wireFormat;

  UsersGrpcClientImpl(GrpcClient client, SocketAddress socketAddress) {
    this(client, socketAddress, io.vertx.grpc.common.WireFormat.PROTOBUF);
  }

  UsersGrpcClientImpl(GrpcClient client, SocketAddress socketAddress, io.vertx.grpc.common.WireFormat wireFormat) {
    this.client = java.util.Objects.requireNonNull(client);
    this.socketAddress = java.util.Objects.requireNonNull(socketAddress);
    this.wireFormat = java.util.Objects.requireNonNull(wireFormat);
  }

  public Future<com.example.app.grpc.proto.UserReply> getUser(com.example.app.grpc.proto.GetUserRequest request) {
    return client.request(socketAddress, GetUser).compose(req -> {
      req.format(wireFormat);
      return req.end(request).compose(v -> req.response().compose(resp -> resp.last()));
    });
  }

  public Future<com.example.app.grpc.proto.UserReply> createUser(com.example.app.grpc.proto.CreateUserRequest request) {
    return client.request(socketAddress, CreateUser).compose(req -> {
      req.format(wireFormat);
      return req.end(request).compose(v -> req.response().compose(resp -> resp.last()));
    });
  }

  public Future<ReadStream<com.example.app.grpc.proto.UserReply>> listUsers(com.example.app.grpc.proto.ListUsersRequest request) {
    return client.request(socketAddress, ListUsers).compose(req -> {
      req.format(wireFormat);
      return req.end(request).compose(v -> req.response().flatMap(resp -> {
        if (resp.status() != null && resp.status() != GrpcStatus.OK) {
          return Future.failedFuture(new io.vertx.grpc.client.InvalidStatusException(GrpcStatus.OK, resp.status()));
        } else {
          return Future.succeededFuture(resp);
        }
      }));
    });
  }

  public Future<com.example.app.grpc.proto.ImportSummary> importUsers(Completable<WriteStream<com.example.app.grpc.proto.CreateUserRequest>> completable) {
    return client.request(socketAddress, ImportUsers)
      .andThen((res, err) -> {
        if (err == null) {
          res.format(wireFormat);
        }
        completable.complete(res, err);
      })
      .compose(request -> {
        return request.response().compose(response -> response.last());
      });
  }

  public Future<ReadStream<com.example.app.grpc.proto.ChatMessage>> chat(Completable<WriteStream<com.example.app.grpc.proto.ChatMessage>> completable) {
    return client.request(socketAddress, Chat)
       .andThen((res, err) -> {
        if (err == null) {
          res.format(wireFormat);
        }
        completable.complete(res, err);
      })
     .compose(req -> {
        return req.response().flatMap(resp -> {
          if (resp.status() != null && resp.status() != GrpcStatus.OK) {
            return Future.failedFuture(new io.vertx.grpc.client.InvalidStatusException(GrpcStatus.OK, resp.status()));
          } else {
            return Future.succeededFuture(resp);
          }
        });
    });
  }
}
