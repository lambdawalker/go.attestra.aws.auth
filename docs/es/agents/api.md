# Referencia de integración HTTP

Las llamadas usan el origen API desplegado, no `appOrigin`. Los tipos de transporte y estados pertenecen al código enlazado y a la [referencia de protocolo](../../../README.md#protocol). El cliente debe almacenar tokens y sesiones temporales de forma segura; nunca en URL o registros.

| Método y ruta | Entrada y autenticación | Resultado y recuperación |
| --- | --- | --- |
| POST `/signup` | `email`, `code_challenge`, `code_challenge_method: S256` | 202 genérico con `request_id`; no garantiza entrega ni existencia de cuenta |
| POST `/resend` | `request_id` | 202 genérico; siguen aplicando límites y espera del servidor |
| POST `/confirm` | `request_id`, `token_b`, exactamente uno de `token_a` o `token_c` | Sesión al confirmar; 422 código incorrecto, 429 intentos, 410 enlace inválido, 409 recuperación |
| POST `/passkeys/options` | Token de acceso Bearer | `creation_options` para el proveedor nativo |
| POST `/passkeys/complete` | Token de acceso Bearer y JSON `credential` | Registro después de aceptación del servidor; cancelar no es éxito |
| POST `/auth/email/start` | `email` | Desafío/sesión Cognito según el tipo del código |
| POST `/auth/email/complete` | `email`, `session`, `code` | Tokens o recuperación del desafío rechazado |
| POST `/auth/passkey/start` | `email` | Desafío/sesión WebAuthn para el proveedor nativo |
| POST `/auth/passkey/complete` | `email`, `session`, JSON `credential` | Tokens tras verificar la aserción |
| POST `/auth/refresh` | `refresh_token` | Tokens renovados o fallo de autenticación |
| POST `/auth/password/set` | Token Bearer; `password` | Valida cuenta verificada y política; Cognito conserva la credencial |
| POST `/auth/password/start` | `email` | Reto PASSWORD y sesión Cognito |
| POST `/auth/password/complete` | `email`, `session`, `password` | Tokens tras verificar contraseña |
| POST `/auth/status` | Token de acceso Bearer | Booleanos `passkey_registered`, `password_registered`, `id_captured`, `onboarding_complete`; 401 sin sesión válida |

Los [handlers de correo](../../../api/handler.go), [passkeys](../../../passkey/handler.go), [inicio de sesión](../../../signin/handler.go) y [tipos del servicio](../../../signin/service.go) definen el comportamiento concreto. Reciben `context.Context` y una solicitud API Gateway v2, y devuelven respuesta v2/error; los puntos Lambda están en `cmd/`. Integra mediante HTTPS, sin tratar los paquetes internos de despliegue como SDK.

Los fallos de passkey se traducen en 401 `sign_in_required`, 400 `invalid_credential` o 503 `temporarily_unavailable`. Los de inicio de sesión: 401 `sign_in_required`, 400 `authentication_failed` o 503 `temporarily_unavailable`. No reutilices una prueba consumida para recuperar sesión; usa inicio de sesión. Leer un enlace por GET no confirma una cuenta.

## Captura e índice

La [referencia de captura](../../id-capture.md#http-contract) define rutas, cuerpos, estados, errores, checksums, revisiones y retención. Obtén primero la política. Conserva las claves de operación ante resultados inciertos; envía bytes a S3 por PUT con las cabeceras exactas y finaliza los identificadores front/back seleccionados. Consulta el estado real; cancelar y borrar evidencia es asíncrono. No cargues imágenes en el cuerpo de una llamada Lambda.

El [contrato del índice](../../environment-index.md#public-response) ofrece GET público `/v1/environments`, `schemaVersion: 1`. POST `/v1/environments/<environment>/changes` requiere AWS SigV4 y permiso IAM del entorno. Es una ruta operativa: no entregues credenciales de despliegue a móviles. `registry/registry.go` define tipos públicos y escrituras condicionales.

Consulta la [política configurable](../../onboarding-policy.md) para valores predeterminados, finalización durable y actualización de clientes.
