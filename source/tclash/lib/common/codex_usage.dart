import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:fl_clash/common/constant.dart';
import 'package:fl_clash/core/core.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:path/path.dart' as p;

typedef UsageJson = Map<String, Object?>;

UsageJson usageObject(Object? value) => value is UsageJson ? value : const {};

Iterable<UsageJson> usageList(Object? value) =>
    value is List ? value.whereType<UsageJson>() : const [];

UsageJson currentUsageActivity(UsageJson data) {
  final activity = usageObject(usageObject(data['telemetry'])['activity']);
  final at = DateTime.tryParse(activity['at']?.toString() ?? '');
  if (at == null) return const {};
  final age = DateTime.now().difference(at);
  return age < const Duration(seconds: 30) && age >= const Duration(minutes: -5)
      ? activity
      : const {};
}

final codexUsageProvider = Provider<CodexUsage>((ref) {
  final usage = CodexUsage(() => ref.read(currentProfileIdProvider));
  ref.onDispose(usage.dispose);
  return usage;
});

class CodexUsage extends ChangeNotifier {
  CodexUsage(this._selectedProfile);

  final int? Function() _selectedProfile;
  Process? _process;
  StreamIterator<String>? _responses;
  Timer? _timer;
  bool _polling = false;
  bool _closed = false;
  bool _changed = false;
  DateTime? _trafficAt;
  int? _trafficProfile;
  List<String> _groups = const [];
  DateTime? _groupsAt;
  String _window = '';
  UsageJson data = const {};
  bool withCache = true;
  bool unavailable = false;

  static bool get supported => Platform.isMacOS || Platform.isWindows;

  void start() {
    if (!supported || _timer != null || _closed) return;
    _timer = Timer.periodic(const Duration(seconds: 3), (_) => refresh());
    unawaited(refresh());
  }

  void select({String? window, bool? cache}) {
    _window = window ?? _window;
    withCache = cache ?? withCache;
    _changed = true;
    notifyListeners();
    unawaited(refresh());
  }

  Future<void> refresh() async {
    if (_closed || _polling) return;
    _polling = true;
    _changed = false;
    try {
      const fixturePath = String.fromEnvironment('TCLASH_CODEX_FIXTURE');
      if (safeModeBuild) {
        if (fixturePath.isNotEmpty) {
          data = usageObject(
            jsonDecode(await File(fixturePath).readAsString()),
          );
        }
      } else {
        await _connect();
        if (_closed) return;
        final window = _window;
        final cache = withCache;
        final result = await _send({'window': window, 'cache': cache});
        if (result['role'] != 'b') throw const FormatException();
        if (window == _window && cache == withCache) data = result;
        await _captureTraffic();
      }
      unavailable = false;
    } catch (_) {
      unavailable = true;
      _disconnect();
    } finally {
      _polling = false;
      if (!_closed) {
        notifyListeners();
        if (_changed) unawaited(refresh());
      }
    }
  }

  Future<void> _connect() async {
    if (_process != null) return;
    final process = await Process.start(
      p.join(
        p.dirname(Platform.resolvedExecutable),
        Platform.isWindows ? 'tclash-collector.exe' : 'tclash-collector',
      ),
      const [],
    );
    if (_closed) {
      process.kill();
      return;
    }
    unawaited(process.stderr.drain<void>());
    _process = process;
    _responses = StreamIterator(
      process.stdout.transform(utf8.decoder).transform(const LineSplitter()),
    );
  }

  Future<UsageJson> _send(UsageJson request) async {
    final process = _process;
    final responses = _responses;
    if (process == null || responses == null) throw const FormatException();
    process.stdin.writeln(jsonEncode(request));
    await process.stdin.flush().timeout(const Duration(seconds: 4));
    if (!await responses.moveNext().timeout(const Duration(seconds: 12))) {
      throw const FormatException();
    }
    return usageObject(jsonDecode(responses.current));
  }

  Future<void> _captureTraffic() async {
    final now = DateTime.now().toUtc();
    if (_closed || now.difference(_trafficAt ?? DateTime(1970)).inSeconds < 5) {
      return;
    }
    _trafficAt = now;
    final core = coreController;
    final profile = _selectedProfile();
    final List<UsageJson> snapshot;
    try {
      if (_groupsAt == null ||
          profile != _trafficProfile ||
          now.difference(_groupsAt!).inSeconds >= 60) {
        final proxies = await core.getProxies().timeout(
          const Duration(seconds: 2),
        );
        _groups = [
          for (final name in proxies.all)
            if (proxies.proxies[name] case {'all': List<dynamic> _}) name,
        ];
        _groupsAt = now;
        _trafficProfile = profile;
      }
      if (_groups.isEmpty) return;
      final connections = await core.getConnections().timeout(
        const Duration(seconds: 2),
      );
      snapshot = [
        for (final connection in connections)
          {
            'id': connection.id,
            'start': connection.start.toUtc().toIso8601String(),
            'upload': connection.upload,
            'download': connection.download,
            'groups': connection.chains,
          },
      ];
    } catch (_) {
      return;
    }
    if (!_closed && _selectedProfile() == profile) {
      await _send({
        'operation': 'traffic',
        'traffic': {
          'at': now.toIso8601String(),
          'profile': profile?.toString() ?? '',
          'groups': _groups,
          'connections': snapshot,
        },
      });
    }
  }

  void _disconnect() {
    final process = _process;
    _process = null;
    if (process != null) process.kill();
    unawaited(_responses?.cancel());
    _responses = null;
  }

  @override
  void dispose() {
    _closed = true;
    _timer?.cancel();
    _disconnect();
    super.dispose();
  }
}
