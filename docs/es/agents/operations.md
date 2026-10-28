# Despliegue, recuperación y desmontaje

| Objetivo | Entrada | Guía autoritativa |
| --- | --- | --- |
| Crear/reanudar entorno | `setup.bat` / `./setup.sh` | [Primer despliegue](../../first-deployment.md) |
| Desplegar solo el índice y la administración compartidos | `deploy.bat -Scope shared` / `./deploy.sh -scope shared` | [Despliegue compartido](../../deployment.md#shared-index-and-admin-deployment) |
| Compilar ZIP de Lambda | `scripts/build.bat` / `./scripts/build.sh` | [Despliegue](../../deployment.md) |
| Previsualizar/actualizar aplicación | `deploy.bat` / `./deploy.sh` o acción Deploy AWS | [GitHub](../../github-deployment.md) |
| Reemplazar tokens, cambiar frase o borrar credenciales | `scripts/credentials.bat` / `./scripts/credentials.sh` | [Credenciales](../../deployment.md) |
| Eliminar un entorno | `scripts/teardown.bat` / `./scripts/teardown.sh` | [Desmontaje de entorno](../../teardown.md) |
| Eliminar índice después de todos los entornos | `scripts/teardown-index.bat` / `./scripts/teardown-index.sh` | [Desmontaje del índice](../../teardown-index.md) |

Setup es reanudable; no hace rollback transaccional. Un error conserva recursos completados y checkpoints privados en `.attestra/`. No borres estado para reparar un bloqueo. Después de una interrupción, confirma que no quede otro escritor antes de seguir la recuperación del recibo. Un recibo local de setup completo no comprueba salud remota; `-force-setup` reconcilia servicios.

Cada entorno tiene un bucket privado y versionado para estado. El índice usa otro bucket por cuenta/región. Ambos usan el backend S3 y secretos con frase de Pulumi; no necesitan token de Pulumi Cloud. Conserva la frase original de cada stack. Las credenciales temporales de AWS expiran; los perfiles renuevan mediante un proveedor CLI aislado. Las credenciales pegadas no se renuevan solas. Consulta la [limitación de rutas del helper Windows](../../../tools/deploy/README.md).

Dev, QA y pruebas personalizadas empiezan con concurrencia Lambda compartida y máximo de dos invocaciones del worker de cola. Producción exige capacidad explícita y control estricto de cuota. No son dos workers provisionados permanentemente. La cuota de cuenta/región se comparte entre stacks; la [guía de capacidad](../../github-deployment.md#capture-concurrency-per-environment) explica configuración y alternativas.

El índice se reutiliza y actualiza después del despliegue. El hash público no es un SHA Git. Una publicación fallida conserva un recibo: reintenta publicar sin redesplegar a ciegas. IAM prueba autorización, no que ocurrió un despliegue. [Recuperación del índice](../../environment-index.md) explica bloqueos y concurrencia.

La comprobación final revisa infraestructura, DNS/TLS, comportamiento sin autenticar, CORS e índice, sin enviar correo ni cargar evidencia. Teardown de entorno retira su entrada y conserva infraestructura compartida. Teardown de índice rechaza filas/bloqueos activos, verifica borrado y conserva estado/historial. Mantén detenidos los despliegues; los escaneos previos no son un bloqueo transaccional distribuido.
