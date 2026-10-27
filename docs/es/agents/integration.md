# Integración con Android y el sistema

El [catálogo central](https://raw.githubusercontent.com/lambdawalker/design.attestra/main/docs/repositories.md) identifica responsables. Backend y requisitos centrales tienen revisiones independientes; aún no existe una matriz de versiones certificada entre repositorios.

Setup exporta `android-config/<environment>.properties`. El [exportador](../../../tools/deploy/internal/android/export.go) escribe estas claves públicas:

| Clave | Significado |
| --- | --- |
| `attestraEnvironment` | Nombre del entorno |
| `attestraApiBaseUrl` | Origen HTTPS de la API |
| `attestraLinkHost` | Host de aplicación/enlaces |
| `attestraAwsRegion` | Región AWS |
| `attestraCognitoUserPoolId` | Identificador público del pool |
| `attestraCognitoClientId` | Identificador público del cliente |
| `attestraCaptureEnabled` | Valor de despliegue; mandan la configuración de ejecución y la política del servidor |
| `attestraCaptureDocumentType` | Tipo de documento configurado |

En Android usa la [opción Gradle documentada](https://github.com/lambdawalker/android.attestra.auth/blob/main/README.md):

```sh
./gradlew :app:assembleDebug -PattestraConfigFile=../go.attestra.aws.auth/android-config/qa.properties
```

Es un ejemplo de compilación entre repositorios. Requiere SDK/toolchain Android y el archivo generado; la construcción de documentación no lo ejecuta. Las propiedades Gradle explícitas reemplazan valores del archivo. Datos ausentes o inválidos fallan la compilación. Una opción del cliente no habilita captura en el servidor.

El backend no hospeda `/verify-email`, la web, `assetlinks.json` ni asociaciones iOS. Prepara ese hosting por separado. Los nombres habituales son `<env>.api.<base-domain>`, `<env>.<base-domain>` y `<env>.info.<base-domain>` para correo. El índice compartido usa `index.<base-domain>`; la consola solo por invitación usa `admin.<base-domain>`.

Consulta la [configuración de ejecución](../../admin-runtime-config.md) en `/v1/app-config` de la API del entorno configurado al iniciar y volver al primer plano. La caché validada permite como máximo 15 minutos de uso anterior ante fallos transitorios; los documentos inválidos deshabilitan la captura. Esto cambia el comportamiento sin modificar rutas, Cognito ni la sesión de autenticación. La creación envía `expected_policy_version` de la política revisada; `409 policy_changed` exige actualizar el aviso y otra acción explícita de inicio.

Este cambio no implementa descubrimiento automático del índice en Android. Usa las propiedades hasta que el cliente incorpore esa función. Permisos de cámara, propiedad de bitmaps, cancelación por ciclo de vida y almacenamiento protegido pertenecen a Android; su [guía de captura](https://github.com/lambdawalker/android.attestra.auth/blob/main/docs/identity-capture.md) enlaza el contrato concreto.
