const _useDevIdentity = bool.fromEnvironment('APP_DEV');
const _useIsolatedSmoke = bool.fromEnvironment('APP_DEV_ISOLATED_SMOKE');
const _isolatedSmokeMixedPort = int.fromEnvironment(
  'APP_DEV_MIXED_PORT',
  defaultValue: 17890,
);

class AppIdentity {
  static const isDev = _useDevIdentity;
  static const isIsolatedSmoke = isDev && _useIsolatedSmoke;
  static const isolatedSmokeMixedPort = _isolatedSmokeMixedPort;

  static const productName = 'Bettbox';
  static const devSuffix = 'Dev';
  static const packageId = 'com.appshub.bettbox';

  static const compactName = isDev ? '$productName$devSuffix' : productName;
  static const displayName = isDev ? '$productName Dev' : productName;
  static const mainExecutableName = productName;
  static const coreExecutableName = '${compactName}Core';
  static const dataDirName = compactName;
  static const tunDeviceName = compactName;
}

class WindowsHelperIdentity {
  static const serviceName = '${AppIdentity.compactName}HelperService';
  static const pipeName = '\\\\.\\pipe\\${AppIdentity.compactName}.Helper';
}
