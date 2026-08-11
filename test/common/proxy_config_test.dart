import 'package:bett_box/common/proxy_config.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('inherits the global fingerprint for supported proxies', () {
    final config = <String, dynamic>{
      'global-client-fingerprint': 'firefox',
      'proxies': [
        {'type': 'trojan'},
        {'type': 'vless', 'tls': true},
        {'type': 'hysteria2'},
      ],
    };

    patchProxyClientFingerprints(config);

    expect(config['proxies'][0]['client-fingerprint'], 'firefox');
    expect(config['proxies'][1]['client-fingerprint'], 'firefox');
    expect(config['proxies'][2]['client-fingerprint'], isNull);
  });

  test('normalizes an inherited Trojan Chrome fingerprint to Firefox', () {
    final config = <String, dynamic>{
      'global-client-fingerprint': 'chrome',
      'proxies': [
        {'type': 'trojan'},
      ],
    };

    patchProxyClientFingerprints(config);

    expect(config['proxies'][0]['client-fingerprint'], 'firefox');
  });

  test('normalizes an explicit Trojan Chrome fingerprint to Firefox', () {
    final config = <String, dynamic>{
      'proxies': [
        {'type': 'trojan', 'client-fingerprint': 'chrome'},
      ],
    };

    patchProxyClientFingerprints(config);

    expect(config['proxies'][0]['client-fingerprint'], 'firefox');
  });

  test('preserves explicit non-Chrome Trojan fingerprints', () {
    final config = <String, dynamic>{
      'global-client-fingerprint': 'chrome',
      'proxies': [
        {'type': 'trojan', 'client-fingerprint': 'safari'},
      ],
    };

    patchProxyClientFingerprints(config);

    expect(config['proxies'][0]['client-fingerprint'], 'safari');
  });

  test('does not rewrite Chrome for other supported protocols', () {
    final config = <String, dynamic>{
      'global-client-fingerprint': 'chrome',
      'proxies': [
        {'type': 'vless', 'tls': true},
        {'type': 'vmess', 'tls': true, 'client-fingerprint': 'chrome'},
        {'type': 'anytls'},
      ],
    };

    patchProxyClientFingerprints(config);

    for (final proxy in config['proxies'] as List) {
      expect(proxy['client-fingerprint'], 'chrome');
    }
  });

  test('ignores malformed proxy collections and rows', () {
    final missingList = <String, dynamic>{'proxies': 'invalid'};
    final malformedRows = <String, dynamic>{
      'global-client-fingerprint': 'chrome',
      'proxies': [null, 'invalid', <String, dynamic>{}],
    };

    patchProxyClientFingerprints(missingList);
    patchProxyClientFingerprints(malformedRows);

    expect(missingList['proxies'], 'invalid');
    expect(malformedRows['proxies'][2]['client-fingerprint'], isNull);
  });
}
