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
import io.vertx.grpc.server.StatusException;

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
public class UsersGrpcService extends UsersService implements Service {

  /**
   * Users service name.
   */
  public static final ServiceName SERVICE_NAME = ServiceName.create("com.example.app.grpc", "Users");

  /**
   * Users service descriptor.
   */
  public static final Descriptors.ServiceDescriptor SERVICE_DESCRIPTOR = UsersProto.getDescriptor().findServiceByName("Users");

  @Override
  public ServiceName name() {
    return SERVICE_NAME;
  }

  @Override
  public Descriptors.ServiceDescriptor descriptor() {
    return SERVICE_DESCRIPTOR;
  }

  @Override
  public void bind(GrpcServer server) {
    builder(this).bind(all()).build().bind(server);
  }

  /**
   * @return a service binding all methods of the given {@code service}
   */
  public static Service of(UsersService service) {
    return builder(service).bind(all()).build();
  }

  /**
   * GetUser protobuf RPC server service method.
   */
  public static final ServiceMethod<com.example.app.grpc.proto.GetUserRequest, com.example.app.grpc.proto.UserReply> GetUser = ServiceMethod.server(
    SERVICE_NAME,
    "GetUser",
    GrpcMessageEncoder.encoder(),
    GrpcMessageDecoder.decoder(com.example.app.grpc.proto.GetUserRequest.newBuilder()));

  /**
   * CreateUser protobuf RPC server service method.
   */
  public static final ServiceMethod<com.example.app.grpc.proto.CreateUserRequest, com.example.app.grpc.proto.UserReply> CreateUser = ServiceMethod.server(
    SERVICE_NAME,
    "CreateUser",
    GrpcMessageEncoder.encoder(),
    GrpcMessageDecoder.decoder(com.example.app.grpc.proto.CreateUserRequest.newBuilder()));

  /**
   * ListUsers protobuf RPC server service method.
   */
  public static final ServiceMethod<com.example.app.grpc.proto.ListUsersRequest, com.example.app.grpc.proto.UserReply> ListUsers = ServiceMethod.server(
    SERVICE_NAME,
    "ListUsers",
    GrpcMessageEncoder.encoder(),
    GrpcMessageDecoder.decoder(com.example.app.grpc.proto.ListUsersRequest.newBuilder()));

  /**
   * ImportUsers protobuf RPC server service method.
   */
  public static final ServiceMethod<com.example.app.grpc.proto.CreateUserRequest, com.example.app.grpc.proto.ImportSummary> ImportUsers = ServiceMethod.server(
    SERVICE_NAME,
    "ImportUsers",
    GrpcMessageEncoder.encoder(),
    GrpcMessageDecoder.decoder(com.example.app.grpc.proto.CreateUserRequest.newBuilder()));

  /**
   * Chat protobuf RPC server service method.
   */
  public static final ServiceMethod<com.example.app.grpc.proto.ChatMessage, com.example.app.grpc.proto.ChatMessage> Chat = ServiceMethod.server(
    SERVICE_NAME,
    "Chat",
    GrpcMessageEncoder.encoder(),
    GrpcMessageDecoder.decoder(com.example.app.grpc.proto.ChatMessage.newBuilder()));

  /**
   * @return a mutable list of the known protobuf RPC server service methods.
   */
  public static java.util.List<ServiceMethod<?, ?>> all() {
    java.util.List<ServiceMethod<?, ?>> all = new java.util.ArrayList<>();
    all.add(GetUser);
    all.add(CreateUser);
    all.add(ListUsers);
    all.add(ImportUsers);
    all.add(Chat);
    return all;
  }


  /**
   * @return a free form builder that gives the opportunity to bind only certain methods of a service
   */
  public static Builder builder(UsersService service) {
    return new Builder(service);
  }

  /**
   * Service builder.
   */
  public static class Builder implements ServiceBuilder {

    private final List<ServiceMethod<?, ?>> serviceMethods = new ArrayList<>();
    private final UsersService instance;

    private Builder(UsersService instance) {
      this.instance = instance;
    }

//    private void validate() {
//      for (ServiceMethod<?, ?> serviceMethod : serviceMethods) {
//        if (resolveHandler(serviceMethod) == null) {
//          throw new IllegalArgumentException("Invalid service method:" + serviceMethod);
//        }
//      }
//    }

    /**
     * Throws {@code UnsupportedOperationException}.
     */
    public <Req, Resp> ServiceBuilder bind(ServiceMethod<Req, Resp> serviceMethod, Handler<GrpcServerRequest<Req, Resp>> handler) {
      throw new UnsupportedOperationException();
    }

    /**
     * @return this builder
     */
    public Builder bind(List<ServiceMethod<?, ?>> methods) {
      serviceMethods.addAll(methods);
      return this;
    }

    /**
     * @return this builder
     */
    public Builder bind(ServiceMethod<?, ?>... methods) {
      return bind(java.util.Arrays.asList(methods));
    }

    public Service build() {
      return new Invoker();
    }

    private class Invoker implements Service {

      // Defensive copy
      private final List<ServiceMethod<?, ?>> serviceMethods = new ArrayList<>(Builder.this.serviceMethods);

      public ServiceName name() {
        return SERVICE_NAME;
      }

      public Descriptors.ServiceDescriptor descriptor() {
        return SERVICE_DESCRIPTOR;
      }

      /**
       * Bind the contained service methods to the {@code server}.
       */
      public void bind(GrpcServer server) {
        for (ServiceMethod<?, ?> serviceMethod : serviceMethods) {
          bindHandler(serviceMethod, server);
        }
      }

      private <Req, Resp> void bindHandler(ServiceMethod<Req, Resp> serviceMethod, GrpcServer server) {
        Handler<io.vertx.grpc.server.GrpcServerRequest<Req, Resp>> handler = resolveHandler(serviceMethod);
        server.callHandler(serviceMethod, handler);
      }

      private <Req, Resp> Handler<io.vertx.grpc.server.GrpcServerRequest<Req, Resp>> resolveHandler(ServiceMethod<Req, Resp> serviceMethod) {
        if (GetUser == serviceMethod) {
          Handler<io.vertx.grpc.server.GrpcServerRequest<com.example.app.grpc.proto.GetUserRequest, com.example.app.grpc.proto.UserReply>> handler = this::handle_getUser;
          Handler<?> handler2 = handler;
          return (Handler<io.vertx.grpc.server.GrpcServerRequest<Req, Resp>>) handler2;
        }
        if (CreateUser == serviceMethod) {
          Handler<io.vertx.grpc.server.GrpcServerRequest<com.example.app.grpc.proto.CreateUserRequest, com.example.app.grpc.proto.UserReply>> handler = this::handle_createUser;
          Handler<?> handler2 = handler;
          return (Handler<io.vertx.grpc.server.GrpcServerRequest<Req, Resp>>) handler2;
        }
        if (ListUsers == serviceMethod) {
          Handler<io.vertx.grpc.server.GrpcServerRequest<com.example.app.grpc.proto.ListUsersRequest, com.example.app.grpc.proto.UserReply>> handler = this::handle_listUsers;
          Handler<?> handler2 = handler;
          return (Handler<io.vertx.grpc.server.GrpcServerRequest<Req, Resp>>) handler2;
        }
        if (ImportUsers == serviceMethod) {
          Handler<io.vertx.grpc.server.GrpcServerRequest<com.example.app.grpc.proto.CreateUserRequest, com.example.app.grpc.proto.ImportSummary>> handler = this::handle_importUsers;
          Handler<?> handler2 = handler;
          return (Handler<io.vertx.grpc.server.GrpcServerRequest<Req, Resp>>) handler2;
        }
        if (Chat == serviceMethod) {
          Handler<io.vertx.grpc.server.GrpcServerRequest<com.example.app.grpc.proto.ChatMessage, com.example.app.grpc.proto.ChatMessage>> handler = this::handle_chat;
          Handler<?> handler2 = handler;
          return (Handler<io.vertx.grpc.server.GrpcServerRequest<Req, Resp>>) handler2;
        }
        return null;
      }


  private void handle_getUser(io.vertx.grpc.server.GrpcServerRequest<com.example.app.grpc.proto.GetUserRequest, com.example.app.grpc.proto.UserReply> request) {
    request.handler(msg -> {
      instance.getUser(msg, (res, err) -> {
        if (err == null) {
          request.response().end(res);
        } else {
          request.response().fail(err);
        }
      });
    });
  }

  private void handle_createUser(io.vertx.grpc.server.GrpcServerRequest<com.example.app.grpc.proto.CreateUserRequest, com.example.app.grpc.proto.UserReply> request) {
    request.handler(msg -> {
      instance.createUser(msg, (res, err) -> {
        if (err == null) {
          request.response().end(res);
        } else {
          request.response().fail(err);
        }
      });
    });
  }

  private void handle_listUsers(io.vertx.grpc.server.GrpcServerRequest<com.example.app.grpc.proto.ListUsersRequest, com.example.app.grpc.proto.UserReply> request) {
    request.handler(msg -> {
      instance.listUsers(msg, request.response());
    });
  }

  private void handle_importUsers(io.vertx.grpc.server.GrpcServerRequest<com.example.app.grpc.proto.CreateUserRequest, com.example.app.grpc.proto.ImportSummary> request) {
    instance.importUsers(request, (res, err) -> {
      if (err == null) {
        request.response().end(res);
      } else {
        request.response().fail(err);
      }
    });
  }

  private void handle_chat(io.vertx.grpc.server.GrpcServerRequest<com.example.app.grpc.proto.ChatMessage, com.example.app.grpc.proto.ChatMessage> request) {
    instance.chat(request, request.response());
  }
    }
  }
}
