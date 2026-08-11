void patchProxyClientFingerprints(Map<String, dynamic> rawConfig) {
  final proxies = rawConfig['proxies'];
  if (proxies is! List) return;

  final globalClientFingerprint = rawConfig['global-client-fingerprint'];
  for (final proxy in proxies) {
    if (proxy is! Map) continue;

    final type = proxy['type']?.toString().toLowerCase();
    final isTls = proxy['tls'] == true;
    final supportsClientFingerprint =
        type == 'trojan' ||
        type == 'anytls' ||
        ((type == 'vmess' || type == 'vless') && isTls);
    if (!supportsClientFingerprint) continue;

    if (globalClientFingerprint != null &&
        proxy['client-fingerprint'] == null) {
      proxy['client-fingerprint'] = globalClientFingerprint;
    }

    // Mihomo's Chrome uTLS fingerprint can omit certificate signature
    // algorithms required by some sing-box Trojan servers. Bettbox
    // historically normalized Chrome to Firefox, but that compatibility
    // guard was removed in an unrelated NTP change. Limit the restored
    // behavior to Trojan so other protocols retain their explicit choice.
    if (type == 'trojan' && proxy['client-fingerprint'] == 'chrome') {
      proxy['client-fingerprint'] = 'firefox';
    }
  }
}
