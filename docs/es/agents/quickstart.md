# Inicio desde el código fuente

Instala Go 1.26.6 o posterior, Git, AWS CLI v2 y Pulumi. Usa tu perfil administrativo de AWS para SSO o inicio de sesión por navegador. Prepara un token de GitHub con los permisos de la [guía de primer despliegue](../../first-deployment.md) y, si eliges Cloudflare, un token restringido a la zona con Zone Read y DNS Edit. No hay un paquete cliente publicado que instalar.

Desde este repositorio:

```sh
go test ./...
go -C tools/deploy test ./...
```

Estas pruebas usan datos de prueba y no crean cuentas, envían correo ni cargan identificaciones. Para un entorno real ejecuta `setup.bat` en Windows o `./setup.sh` en Linux/macOS. El asistente crea recursos con costo después de sus preguntas. Elige primero el entorno, autentícate y selecciona Route 53 o Cloudflare. El almacenamiento opcional de credenciales usa una bóveda local cifrada; conserva su frase de acceso por separado.

Setup prepara estado, índice compartido, IAM/GitHub, prerrequisitos SES/ACM, DNS y esperas de preparación. Después compila y despliega la aplicación, publica configuración, comprueba salud y exporta propiedades Android. Los prerrequisitos deben terminar antes que sus dependientes. La [secuencia completa](../../first-deployment.md) explica recuperación y requisitos.

Como demostración de API sin nube, ejecuta las [pruebas de handlers](../../../api/handler_test.go):

```sh
go test ./api ./email ./passkey ./signin ./captureapi ./registry
```

Ejercitan handlers y servicios reales con dependencias falsas. No son un servidor HTTP local, simulador SES ni emulador Android. El resultado esperado es que pasen las pruebas de Go. [Cobertura](../../coverage.md) enlaza cada caso con su código.

Después de setup, revisa y confirma los YAML de Pulumi generados; sus secretos cifrados no son texto plano. Conserva las frases y recibos de `.attestra/` en privado. Carga `android-config/<environment>.properties` mediante `-PattestraConfigFile` en Android. Prueba luego correo, inicio de sesión y captura habilitada deliberadamente: la comprobación de salud no ejecuta esos flujos.
