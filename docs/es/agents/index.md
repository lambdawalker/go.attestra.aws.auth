# Guía de integración del backend

Alcance: código actual de `main`, revisado en `64f4faea74e71a82795560e4b43e195d820a5242`. Es un servicio desplegable y una herramienta de operación, no un SDK cliente de Go publicado. Consulta la [instalación desde código](../../../IMPORT.md).

| Tarea | Siguiente lectura | Responsable |
| --- | --- | --- |
| Crear o actualizar un entorno | [Inicio rápido](quickstart.md), [operación](operations.md) | `tools/deploy/internal/workflow` |
| Integrar correo, passkeys y sesiones | [API HTTP](api.md) | `api`, `passkey`, `signin` |
| Capturar una identificación | [Contrato de captura](../../id-capture.md) | `capture`, `captureapi`, `awscapture` |
| Descubrir entornos o exportar configuración Android | [Integración](integration.md) | `registry`, `internal/android` |
| Recuperar un despliegue o eliminar recursos | [Operación](operations.md) | Desmontaje de entorno frente a índice compartido |
| Evaluar preparación | [Limitaciones](limitations.md), [cobertura](../../coverage.md) | Pruebas y evidencia explícita |

Una respuesta genérica de registro no demuestra entrega de correo. Solo el cliente que confirma recibe la sesión. Capture `ready` significa evidencia decodificada y fijada, no campos extraídos ni identidad verificada. Nunca registres pruebas, tokens, imágenes o URL prefirmadas. El índice publica configuración; no autentica usuarios ni supervisa salud.

Los requisitos generales pertenecen a [la guía central](https://raw.githubusercontent.com/lambdawalker/design.attestra/main/docs/agents/index.md), [onboarding](https://raw.githubusercontent.com/lambdawalker/design.attestra/main/auth/onboarding/architecture.md) y [contratos de evidencia](https://raw.githubusercontent.com/lambdawalker/design.attestra/main/auth/onboarding/id-evidence-contracts.md). La serialización exacta pertenece aquí; no deduzcas rutas de un prototipo.
