# Limitaciones y diagnóstico

| Síntoma o límite | Interpretación y siguiente paso |
| --- | --- |
| Falta `dist/capture.zip` | Compila los archivos antes del preview o usa el asistente; Pulumi no los compila |
| Identidad SES sin verificar | Revisa región y DNS; signup aceptado no demuestra entrega |
| Cloudflare HTTP 400 en autorización | Introduce solo el token, sin `Bearer`, y revisa permisos de zona |
| Timeout del índice | Revisa URL directa/pública y último fallo HTTP, DNS o TLS; conserva recursos para reanudar |
| Token AWS expirado | Renueva perfil CLI o reemplaza credenciales temporales pegadas; conserva estado |
| Cuota de concurrencia reservada | Revisa cuota de cuenta/región y política del entorno; cero no significa sin reserva |
| Recibo antiguo después de recrear | Setup archiva los completos; recupera primero un teardown inconcluso |
| Salud correcta, correo/login falla | Las sondas no crean usuarios, entregan correo, usan passkeys ni cargan documentos |

La extracción visual con Bedrock Gemma 4 E2B y la persistencia de las correcciones del usuario están implementadas; el acceso al modelo y la precisión requieren pruebas reales. La validación independiente y la verificación de domicilio siguen diferidas. Las comprobaciones de archivos existen, pero captura real está deshabilitada por defecto y requiere política aprobada y pruebas S3/dispositivo. La política actual admite exactamente front/back, no documentos arbitrarios.

El índice admite una cuenta/región por instancia. Aislamiento productivo entre cuentas y rol central de publicación requieren diseño adicional. Cambios directos en Pulumi/AWS evitan la sincronización. Una interrupción puede dejar visible la última configuración publicada. Cognito permite `PASSWORD` aunque la interfaz use passkeys/correo; no existe una garantía de prohibición de contraseña a nivel proveedor.

Pruebas Go y mocks Pulumi no certifican seguridad desplegada. El [registro central](https://raw.githubusercontent.com/lambdawalker/design.attestra/main/docs/compatibility.md) conserva el reporte del operador de dev/QA del 2026-10-10; no es certificación de versión. Añade refs, dispositivo/región y caso efectivamente probado al registrar evidencia.

Migración: los recibos antiguos de la raíz pasan a `.attestra/` cuando es seguro; conflictos y bloqueos detienen la migración. No confirmes en Git recibos, credenciales, tokens de recuperación o metadatos operativos privados. La configuración pública Android no contiene credenciales. Las frases secretas deben permanecer fuera de Git aunque los YAML cifrados se confirmen.
