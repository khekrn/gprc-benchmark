package com.example.app.grpc.proto;

import io.vertx.core.Future;
import io.vertx.core.Completable;
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
 * <p>Provides support for RPC methods implementations of the Users gRPC service.</p>
 *
 * <p>The following methods of this class should be overridden to provide an implementation of the service:</p>
 * <ul>
 *   <li>GetUser</li>
 *   <li>CreateUser</li>
 *   <li>ListUsers</li>
 *   <li>ImportUsers</li>
 *   <li>Chat</li>
 * </ul>
 */
public class UsersService implements Users {

  /**
   * Override this method to implement the GetUser RPC.
   */
  public Future<com.example.app.grpc.proto.UserReply> getUser(com.example.app.grpc.proto.GetUserRequest request) {
    throw new UnsupportedOperationException("Not implemented");
  }

  protected void getUser(com.example.app.grpc.proto.GetUserRequest request, Completable<com.example.app.grpc.proto.UserReply> response) {
    getUser(request).onComplete(response);
  }

  /**
   * Override this method to implement the CreateUser RPC.
   */
  public Future<com.example.app.grpc.proto.UserReply> createUser(com.example.app.grpc.proto.CreateUserRequest request) {
    throw new UnsupportedOperationException("Not implemented");
  }

  protected void createUser(com.example.app.grpc.proto.CreateUserRequest request, Completable<com.example.app.grpc.proto.UserReply> response) {
    createUser(request).onComplete(response);
  }

  /**
   * Override this method to implement the ListUsers RPC.
   */
  public Future<ReadStream<com.example.app.grpc.proto.UserReply>> listUsers(com.example.app.grpc.proto.ListUsersRequest request) {
    throw new UnsupportedOperationException("Not implemented");
  }

  protected void listUsers(com.example.app.grpc.proto.ListUsersRequest request, WriteStream<com.example.app.grpc.proto.UserReply> response) {
    listUsers(request)
      .onComplete(ar -> {
        if (ar.succeeded()) {
          ReadStream<com.example.app.grpc.proto.UserReply> stream = ar.result();
          stream.pipeTo(response);
        } else {
          // Todo
        }
      });
  }

  /**
   * Override this method to implement the ImportUsers RPC.
   */
  public Future<com.example.app.grpc.proto.ImportSummary> importUsers(ReadStream<com.example.app.grpc.proto.CreateUserRequest> request) {
    throw new UnsupportedOperationException("Not implemented");
  }

  protected void importUsers(ReadStream<com.example.app.grpc.proto.CreateUserRequest> request, Completable<com.example.app.grpc.proto.ImportSummary> response) {
    importUsers(request).onComplete(response);
  }

  /**
   * Override this method to implement the Chat RPC.
   */
  public Future<ReadStream<com.example.app.grpc.proto.ChatMessage>> chat(ReadStream<com.example.app.grpc.proto.ChatMessage> request) {
    throw new UnsupportedOperationException("Not implemented");
  }

  protected void chat(ReadStream<com.example.app.grpc.proto.ChatMessage> request, WriteStream<com.example.app.grpc.proto.ChatMessage> response) {
    chat(request)
      .onComplete(ar -> {
        if (ar.succeeded()) {
          ReadStream<com.example.app.grpc.proto.ChatMessage> stream = ar.result();
          stream.pipeTo(response);
        } else {
          // Todo
        }
      });
  }
}
