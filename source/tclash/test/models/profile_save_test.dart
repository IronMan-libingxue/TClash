import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/models/models.dart';
import 'package:fl_clash/state.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:riverpod/riverpod.dart';

import '../helpers/app_paths.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  late Directory tempDir;

  setUpAll(() async {
    tempDir = Directory.systemTemp.createTempSync('profile_save_test');
    await useAppPaths(tempDir);
  });

  tearDownAll(() {
    try {
      tempDir.deleteSync(recursive: true);
    } catch (_) {}
  });

  group('Profile.saveFile', () {
    // EditProfileView relies on this throwing rather than silently
    // succeeding, so a caller-level try/catch can surface the failure.
    test('rejects an invalid config without touching the saved file', () async {
      final profile = Profile.normal(label: 'p');
      final bytes = Uint8List.fromList(utf8.encode('bad: ['));

      await expectLater(
        profile.saveFile(bytes, validate: (_, _) async => 'invalid config'),
        throwsA(
          isA<MessageException>().having(
            (e) => e.message,
            'message',
            'invalid config',
          ),
        ),
      );

      final savedFile = await profile.file;
      expect(await savedFile.length(), 0);
    });

    test('copies validated bytes and stamps the update time', () async {
      final profile = Profile.normal(label: 'p');
      final bytes = Uint8List.fromList(utf8.encode('proxies: []'));

      final saved = await profile.saveFile(
        bytes,
        validate: (_, _) async => null,
      );

      expect(saved.lastUpdateDate, isNotNull);
      final savedFile = await profile.file;
      expect(await savedFile.readAsString(), 'proxies: []');
    });

    test('validates with the profile age key', () async {
      final profile = Profile.normal(
        label: 'p',
      ).copyWith(ageSecretKey: 'AGE-SECRET-KEY-1A');
      final bytes = Uint8List.fromList(utf8.encode('proxies: []'));
      String? ageSecretKey;

      await profile.saveFile(
        bytes,
        validate: (_, key) async {
          ageSecretKey = key;
          return null;
        },
      );

      expect(ageSecretKey, 'AGE-SECRET-KEY-1A');
    });

    test(
      'an existing subscription still downloads and saves plain YAML',
      () async {
        final overrides = HttpOverrides.current;
        HttpOverrides.global = null;
        addTearDown(() => HttpOverrides.global = overrides);
        final container = ProviderContainer();
        addTearDown(container.dispose);
        globalState.container = container;
        globalState.packageInfo = PackageInfo(
          appName: 'TClash',
          packageName: 'local.tclash.test',
          version: '2.5.13',
          buildNumber: '1',
        );
        final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
        addTearDown(() => server.close(force: true));
        var response = 'proxies: []\nrules: ["MATCH,DIRECT"]\n';
        server.listen((request) async {
          request.response.write(response);
          await request.response.close();
        });
        final profile = Profile.normal(
          label: 'Existing subscription',
          url: 'http://127.0.0.1:${server.port}/profile',
        );
        Future<String?> validate(String path, String key) async {
          expect(await File(path).readAsString(), response);
          return null;
        }

        final imported = await profile.update(validate: validate);
        expect(imported.url, profile.url);
        expect(await (await imported.file).readAsString(), response);
        response = 'proxies: []\nrules: ["MATCH,REJECT"]\n';
        final updated = await imported.update(validate: validate);
        expect(updated.id, imported.id);
        expect(await (await updated.file).readAsString(), response);
      },
    );
  });
}
