import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:bett_box/common/common.dart';
import 'package:bett_box/state.dart';
import 'package:bett_box/utils/platform_check.dart';
import 'package:restart_app/restart_app.dart';

class ExternalControl {
  static const _commandTimeout = Duration(seconds: 2);

  static ServerSocket? _server;
  static TransportType? _transportType;

  static Future<void> start() async {
    if (!system.isDesktop || _server != null) return;

    _transportType = await PlatformChecker.getRecommendedTransport();

    if (_transportType == TransportType.unixSocket) {
      try {
        await _startUnixSocket();
        return;
      } catch (e) {
        commonPrint.log(
          'ExternalControl UDS bind failed, falling back to TCP: $e',
        );
      }
    }
    await _startTcpSocket();
  }

  static Future<void> _startUnixSocket() async {
    final socketPath = await appPath.controlSocketPath;
    final type = FileSystemEntity.typeSync(socketPath);
    if (type != FileSystemEntityType.notFound) {
      try {
        await File(socketPath).delete();
      } catch (_) {}
    }
    final address = InternetAddress(socketPath, type: InternetAddressType.unix);
    _server = await ServerSocket.bind(address, 0);
    _listen();
  }

  static Future<void> _startTcpSocket() async {
    _server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    final portFilePath = await appPath.controlPortFilePath;
    try {
      await File(portFilePath).writeAsString('${_server!.port}');
    } catch (_) {}
    _listen();
  }

  static void _listen() {
    _server!.listen(
      (socket) {
        socket
            .cast<List<int>>()
            .transform(utf8.decoder)
            .transform(const LineSplitter())
            .listen(
              (command) => unawaited(_handleCommand(socket, command)),
              onError: (e) {
                commonPrint.log('ExternalControl command read error: $e');
                socket.destroy();
              },
              cancelOnError: true,
            );
      },
      onError: (e) => commonPrint.log('ExternalControl server error: $e'),
    );
  }

  static Future<void> stop() async {
    await _server?.close();
    _server = null;
    _transportType = null;

    final socketPath = await appPath.controlSocketPath;
    final type = FileSystemEntity.typeSync(socketPath);
    if (type != FileSystemEntityType.notFound) {
      try {
        await File(socketPath).delete();
      } catch (_) {}
    }

    final portFilePath = await appPath.controlPortFilePath;
    if (await File(portFilePath).exists()) {
      try {
        await File(portFilePath).delete();
      } catch (_) {}
    }
  }

  static Future<void> sendCommand(String command) async {
    if (!system.isDesktop) return;

    Object? lastError;

    // Prefer Unix Domain Socket when the socket file exists.
    final socketPath = await appPath.controlSocketPath;
    final socketType = FileSystemEntity.typeSync(socketPath);
    if (socketType != FileSystemEntityType.notFound) {
      try {
        await _sendUnixCommand(socketPath, command);
        return;
      } catch (e) {
        lastError = e;
      }
    }

    // Fall back to TCP loopback port file.
    final portFilePath = await appPath.controlPortFilePath;
    if (await File(portFilePath).exists()) {
      final content = await File(portFilePath).readAsString();
      final port = int.tryParse(content.trim());
      if (port != null) {
        try {
          await _sendTcpCommand(port, command);
          return;
        } catch (e) {
          lastError = e;
        }
      }
    }

    throw StateError(
      lastError == null
          ? 'Bettbox is not running'
          : 'Bettbox control command was not acknowledged: $lastError',
    );
  }

  static Future<void> _sendUnixCommand(
    String socketPath,
    String command,
  ) async {
    final address = InternetAddress(socketPath, type: InternetAddressType.unix);
    final socket = await Socket.connect(
      address,
      0,
    ).timeout(_commandTimeout);
    await _exchangeCommand(socket, command);
  }

  static Future<void> _sendTcpCommand(int port, String command) async {
    final socket = await Socket.connect(
      InternetAddress.loopbackIPv4,
      port,
    ).timeout(_commandTimeout);
    await _exchangeCommand(socket, command);
  }

  static Future<void> _exchangeCommand(Socket socket, String command) async {
    try {
      socket.write('$command\n');
      await socket.flush().timeout(_commandTimeout);
      final response = await socket
          .cast<List<int>>()
          .transform(utf8.decoder)
          .transform(const LineSplitter())
          .first
          .timeout(_commandTimeout);
      final expected = 'ok:$command';
      if (response != expected) {
        throw StateError('Unexpected control response: $response');
      }
    } finally {
      try {
        await socket.close().timeout(_commandTimeout);
      } on TimeoutException {
        socket.destroy();
      } catch (_) {
        socket.destroy();
      }
    }
  }

  static Future<void> _sendResponse(Socket socket, String response) async {
    socket.write('$response\n');
    await socket.flush().timeout(_commandTimeout);
    try {
      await socket.close().timeout(_commandTimeout);
    } on TimeoutException {
      socket.destroy();
    }
  }

  static Future<void> _handleCommand(Socket socket, String command) async {
    final normalized = command.trim();
    final knownCommand = const {'exit', 'restart', 'show'}.contains(normalized);
    try {
      await _sendResponse(
        socket,
        knownCommand ? 'ok:$normalized' : 'error:unknown_command',
      );
    } catch (e) {
      commonPrint.log('ExternalControl response failed: $e');
    }

    switch (normalized) {
      case 'exit':
        unawaited(globalState.appController.handleExit());
      case 'restart':
        Restart.restartApp();
      case 'show':
        await window?.show();
      default:
        commonPrint.log('ExternalControl unknown command: $command');
    }
  }
}
