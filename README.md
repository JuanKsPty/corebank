# corebank

Sistema de banca en línea con **ledger contable de doble entrada** sobre
[TigerBeetle](https://tigerbeetle.com), backend en Go y asistente de IA que opera
las cuentas en lenguaje natural a través del
[Model Context Protocol](https://modelcontextprotocol.io).

**Demo en vivo:** <https://corebank.juank.tech> — entra con las
[credenciales de prueba](#credenciales-de-prueba), que la pantalla de acceso ofrece
en un botón.

---

## Levantarlo

```sh
git clone https://github.com/JuanKsPty/corebank.git
cd corebank
cp .env.example .env
docker compose up
```

La interfaz queda en <http://localhost:5173> y la API en <http://localhost:8080>.
No hace falta editar el `.env`: cada valor tiene un default que funciona, incluida
la clave de IA, que puede quedar vacía.

El arranque en frío importa el dataset completo —1000 usuarios, 1605 cuentas, 6429
movimientos— en unos 2 segundos. El segundo arranque no lo repite: el seeder
encuentra su marca y sale sin tocar nada.

> **Memoria de Docker: 4 GiB mínimo, 6 GiB recomendado.** TigerBeetle reserva
> ~2,3 GiB al arrancar independientemente de `--cache-grid`, y con Postgres al
> lado un Docker limitado a 4 GiB va justo.

### Solo las bases de datos, para desarrollar

```sh
docker compose -f docker-compose.dev.yml up -d
```

Levanta únicamente PostgreSQL y TigerBeetle, para correr la API con `go run` y la
interfaz con `npm run dev`. Comparte los volúmenes con el stack completo, así que
los datos son los mismos y no hay que sembrar dos veces.

---

## Credenciales de prueba

Usuarios reales del dataset, con saldos verificables contra el JSON de origen. Las
contraseñas siguen el patrón del fixture y se publican a propósito: sin ellas nadie
puede evaluar el sistema.

| Correo | Contraseña | Cuentas | Disponible |
|---|---|---|---|
| `ihernandez@email.com` | `Isabel2024!` | 1 | $32,354.53 |
| `andres.perez368@mail.com` | `Andrés2024!` | 3 | $86,047.62 |

El segundo tiene varias cuentas, que es lo que permite probar un traspaso entre
cuentas propias.

---

## Qué tiene de particular

### El sobregiro es imposible por construcción, no por validación

Las cuentas de cliente se crean en TigerBeetle con la regla
`debits_must_not_exceed_credits`, así que un retiro por encima del saldo lo rechaza
la propia base de datos financiera. **No hay un `if saldo < monto` en el código de
aplicación** que alguien pueda olvidar, mover de sitio o dejar fuera de una ruta
nueva.

La contrapartida obligatoria es una cuenta de patrimonio (`world`): en contabilidad
de doble entrada un depósito no puede ser un asiento único, el dinero tiene que
venir de algún lado.

| Operación | Débito | Crédito |
|---|---|---|
| Apertura | `world` | cuenta cliente |
| Depósito | `world` | cuenta cliente |
| Retiro | cuenta cliente | `world` |
| Transferencia | cuenta origen | cuenta destino |

### Un saldo son tres números

Y eso es lo que la interfaz muestra, porque es lo que el ledger tiene:

- **liquidado** — lo que ya asentó
- **retenido** — fondos reservados por un movimiento que nadie ha confirmado aún
- **disponible** — liquidado menos retenido, lo que de verdad se puede gastar

La mayoría de las interfaces bancarias no pueden expresar el del medio, porque
guardan el saldo en una columna. Aquí el saldo **siempre** se lee de TigerBeetle:
**Postgres no tiene columna de saldo**, y es deliberado — no existe la posibilidad
de que dos almacenes discrepen sobre cuánto dinero hay.

### La confirmación de acciones críticas es un paso de servidor

Cuando el asistente propone mover dinero, el backend crea un transfer **pendiente**
en TigerBeetle: los fondos quedan reservados, el saldo disponible ya lo refleja, y
no llegan a su destino. Según lo que haga el cliente, el transfer se **postea** (el
dinero se mueve) o se **anula** (la reserva se libera). Si no responde, **el propio
TigerBeetle libera la reserva al expirar el plazo**, sin ningún proceso de limpieza.

Eso da dos garantías que una tabla de "pendientes" no da gratis: los fondos están
garantizados en el momento de confirmar, y **el modelo de IA no puede mover dinero
por sí solo**. La confirmación es un botón contra un endpoint, no una decisión del
modelo — aunque alguien lograra manipularlo, no hay camino de ahí al dinero.

---

## Arquitectura

```mermaid
flowchart LR
    WEB["web/<br/>Vite + React + TS"]
    API["api/<br/>Go"]
    MCP["servidor MCP<br/>in-process"]
    LLM["Anthropic<br/>(opcional)"]
    TB[("TigerBeetle<br/>DINERO")]
    PG[("PostgreSQL<br/>METADATOS")]

    WEB -->|"REST + SSE"| API
    API --> MCP
    MCP -.-> LLM
    API --> TB
    API --> PG
```

| | TigerBeetle | PostgreSQL |
|---|---|---|
| Es la fuente de verdad de | saldos y movimientos de dinero | identidad, credenciales, metadatos |
| Almacena | cuentas con débitos/créditos posteados y pendientes, transfers de doble entrada, montos en centavos enteros | usuarios, números de cuenta, descripciones, tipos, estados, historial de auditoría, conversaciones |
| No puede | guardar texto, hacer consultas ad-hoc ni joins | garantizar invariantes contables |

### La doble escritura

TigerBeetle tiene el dinero y Postgres los metadatos. **No hay transacción
distribuida**, así que el orden importa:

1. Validar entrada y autorización — la cuenta origen debe pertenecer al usuario del token.
2. `INSERT` en `transactions` con `status='pending'`. **El UUID de esa fila *es* el id del transfer en TigerBeetle.**
3. Enviar el transfer a TigerBeetle.
4. Registrar el resultado: `completed`, o `failed` con el código del ledger.

Si el proceso muere entre 2 y 4 queda una fila `pending` sin ambigüedad, y un
*sweeper* al arrancar consulta esos ids en TigerBeetle y reconcilia. Nunca se pierde
ni se duplica dinero, porque el id es determinista.

---

## El asistente de IA

Seis herramientas expuestas por un **servidor MCP que corre dentro del mismo
binario**, conectado al cliente por un transporte en memoria. Es MCP real —mismo
protocolo, mismos esquemas— sin añadir un contenedor al compose. Las herramientas no
reimplementan lógica: llaman a los mismos servicios de dominio que los handlers REST.

| Herramienta | Requiere confirmación |
|---|---|
| `list_accounts` | no |
| `get_balance` | no |
| `list_transactions` | no |
| `deposit` | no — no saca dinero del cliente |
| `prepare_withdrawal` | **sí** — reserva fondos |
| `prepare_transfer` | **sí** — reserva fondos |

### La IA nunca decide de quién es el dinero

**Ninguna herramienta acepta un `user_id` como argumento.** La identidad viene del
token del request y se inyecta en el handler fuera del alcance del modelo. Si el
modelo inventa un número de cuenta ajeno como origen, la herramienta devuelve error
de autorización. Puede nombrar cualquier cuenta como *destino* —eso es una
transferencia legítima— pero jamás como origen.

El segundo vector es la **inyección de prompt**: las descripciones del historial son
texto que entra al contexto del modelo. La mitigación definitiva no es filtrar
texto, es que la confirmación sea un paso de servidor.

### Sin clave de API también funciona

`ANTHROPIC_API_KEY` es la única dependencia externa del sistema, y es opcional. Sin
ella el asistente responde con un proveedor **basado en reglas** que maneja las
mismas seis herramientas por el mismo servidor MCP y el mismo flujo de confirmación.
La interfaz dice cuál motor está activo — «Modelo claude-sonnet-5» o «Sin IA
configurada» — porque presentar un fallback de reglas como IA sería mentir sobre el
producto.

### Conversaciones para probar

```
¿Cuánto dinero tengo?
Muéstrame mis últimos 5 movimientos
Ingresa $200 en mi cuenta
Retira $50                                   → tarjeta de confirmación
Transfiere $100 a la cuenta 4001-6629-5214-0685
Transfiere $999,999 a 4001-…                 → fondos insuficientes, sin reserva colgada
Transfiere $50 a la cuenta 9999-9999-9999-9999 → cuenta destino no encontrada
Transfiere $50                               → pregunta a qué cuenta
Pasa $200 de mi cuenta de ahorros a la corriente
```

Después de preparar un movimiento, **deja pasar dos minutos sin responder**: la
reserva se libera sola y el saldo disponible vuelve. Eso lo hace TigerBeetle, no
código de la aplicación.

---

## API

Errores en un formato único: `{"error":{"code":"…","message":"…","fields":{…},"request_id":"…"}}`.

| Método | Ruta | Notas |
|---|---|---|
| `GET` | `/healthz` | Sin autenticar, fuera de `/api`: el healthcheck no tiene credenciales. Reporta Postgres y TigerBeetle. |
| `POST` | `/api/auth/register` | Crea usuario **y su primera cuenta bancaria**. Devuelve tokens. |
| `POST` | `/api/auth/login` | Con rate limit. |
| `POST` | `/api/auth/refresh` | Rota el refresh token. |
| `POST` | `/api/auth/logout` | Revoca el refresh token. |
| `GET` | `/api/me` | Perfil y cuentas. |
| `GET` | `/api/dashboard/summary` | Totales, recientes y series para la gráfica. |
| `GET` | `/api/accounts` | Cuentas del usuario, con saldo leído de TigerBeetle. |
| `POST` | `/api/accounts` | Abre una cuenta adicional. Máximo 6 por cliente. |
| `GET` | `/api/accounts/{number}` | Detalle. |
| `GET` | `/api/accounts/{number}/balance` | Saldo puntual. |
| `GET` | `/api/accounts/{number}/transactions` | Historial de la cuenta, paginado por cursor. |
| `GET` | `/api/transactions` | Historial consolidado, con filtros y paginación por cursor. |
| `POST` | `/api/transactions/deposit` | Acepta `Idempotency-Key`. |
| `POST` | `/api/transactions/withdraw` | Acepta `Idempotency-Key`. |
| `POST` | `/api/transactions/transfer` | Acepta `Idempotency-Key`. Valida la cuenta destino. |
| `POST` | `/api/transactions/{hold_id}/confirm` | Postea el transfer pendiente: el dinero se mueve. |
| `POST` | `/api/transactions/{hold_id}/cancel` | Anula el transfer pendiente: la reserva se libera. |
| `GET` | `/api/chat` | Historial de la conversación y qué motor responde. |
| `POST` | `/api/chat` | Mensaje al asistente. Responde por **SSE**. Rate limit propio. |
| `DELETE` | `/api/chat` | Borra la conversación. |

Confirmar y cancelar viven bajo `/api/transactions` y no bajo `/api/chat` a
propósito: son operaciones bancarias, no de conversación, y funcionan igual venga la
propuesta del chat o de cualquier otro sitio.

### Un cliente no puede ver la cuenta de otro

Los intentos de acceso cruzado devuelven **404, nunca 403**. Un 403 confirmaría que
la cuenta existe, y con eso se puede enumerar el banco probando números.

---

## Variables de entorno

Todo tiene un default que funciona. `cp .env.example .env` y listo.

| Variable | Default | Para qué |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Dónde escucha la API. |
| `DATABASE_URL` | `postgres://corebank:corebank@localhost:5432/corebank?sslmode=disable` | Postgres. |
| `TB_ADDRESSES` | `127.0.0.1:3001` | El ledger. **Solo IP:puerto** — su cliente rechaza hostnames. |
| `TB_CLUSTER_ID` | `0` | Cluster fijo para que el ledger sea reproducible desde un clon limpio. |
| `JWT_SECRET` | *(vacío)* | Firma los access tokens; mínimo 32 caracteres. Vacío, la API genera uno por proceso: todo funciona, pero las sesiones no sobreviven un reinicio. **Sin valor por defecto en el repo: un secreto de firma commiteado no es un secreto.** |
| `ANTHROPIC_API_KEY` | *(vacío)* | Única dependencia externa, y opcional. Vacía, el chat responde con reglas locales. |
| `ANTHROPIC_MODEL` | `claude-sonnet-5` | |
| `CONFIRMATION_TTL` | `2m` | Cuánto retiene una reserva antes de que TigerBeetle la libere. |
| `AI_MAX_TOOL_TURNS` | | Acota el loop agéntico para que un modelo confundido no gire indefinidamente. |
| `ENV` | `development` | `production` pasa los logs a JSON. |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` | |
| `LOGIN_RATE_LIMIT` | `10` | Intentos de login por ventana. |
| `BCRYPT_COST` | | Coste del hash de contraseñas. |
| `ACCESS_TOKEN_TTL` / `REFRESH_TOKEN_TTL` | | Vida de los tokens. |
| `CORS_ORIGINS` | `http://localhost:5173` | Solo para `npm run dev`: en el stack de contenedores nginx sirve la SPA y proxea `/api` en el mismo origen, así que no hay CORS. |
| `DB_MAX_CONNS` / `DB_CONNECT_WAIT` / `TB_CONNECT_WAIT` | | Pool y esperas de arranque. |
| `HTTP_SHUTDOWN_TIMEOUT` | | Margen del apagado ordenado. |
| `SEED_FILE` | `seed/data/hnl-seed.json` | Dataset a importar. |

---

## Decisiones sobre el dataset

El JSON de prueba **no es internamente consistente**, y esto se verificó sobre los
datos antes de escribir el importador:

- Si `initial_balance` fuera el saldo de **apertura** y se reprodujeran los 6429
  movimientos en orden cronológico: **312 sobregiros en 126 cuentas**, 70 cuentas
  terminan en negativo, mínimo −$10,933.69.
- Si se despeja el saldo de apertura para que el final coincida con
  `initial_balance`: **140 de 1605 cuentas** siguen quedando en negativo en algún
  punto; harían falta $599,702.45 de ajustes y 88 cuentas necesitarían apertura
  negativa.

TigerBeetle rechaza ambos escenarios con `exceeds_credits`. No se pueden tener las
tres propiedades a la vez: saldo final igual a `initial_balance`, historial completo
dentro del ledger, y cero sobregiros.

**Decisión: `initial_balance` es el saldo actual al momento del seed.**

- El ledger arranca con **un transfer de apertura por cuenta** por ese monto, así que
  los saldos que se ven coinciden **exactamente** con el fixture.
- Los **6429 movimientos históricos** se cargan en Postgres como historial de
  auditoría (`source='seed'`), que es donde de todos modos tienen que vivir sus
  descripciones en español y sus estados.
- **Todo movimiento nuevo** pasa por TigerBeetle como doble entrada real.

### Los 20 correos duplicados

El dataset trae 20 direcciones repetidas que corresponden a **personas distintas**
—UUID y segundo apellido distintos—, así que fusionarlas dejaría cuentas huérfanas.
El correo sigue siendo `UNIQUE` y las colisiones se reescriben como
`usuario+dup2@dominio`, registrado en el log del seeder. Se conservan los 1000
usuarios y las 1605 cuentas mantienen dueño.

### Las contraseñas del seed

Los 1000 usuarios comparten solo **43 contraseñas distintas** (patrón
`Nombre2024!`). Hashear las 43 una vez y reutilizar baja el arranque de ~60 s a ~2 s
con bcrypt. **Es una optimización exclusiva de datos semilla**: todo registro real
recibe su propio salt.

---

## Otras decisiones técnicas

### Los montos son centavos enteros, nunca floats

Ningún monto se representa como coma flotante en ningún punto: centavos en Postgres
(`BIGINT`), en TigerBeetle (`u128`) y por la API. El parseo trabaja sobre el **texto**
decimal en lugar de sobre un `float64`, porque la conversión ingenua pierde dinero:

```
float64(8.87) * 100  ->  886 centavos   (un centavo perdido)
parseo de "8.87"     ->  887 centavos
```

Hay un test que fija esa diferencia para que nadie "simplifique" el parseo a la
versión rota más adelante.

### Identificadores deterministas

El id de una cuenta en el ledger se **deriva** de su número
(`4001-6588-5247-0001` → `4001658852470001`) en lugar de generarse. El id de un
movimiento es el UUID de su fila en Postgres, que son exactamente los 128 bits que
TigerBeetle necesita. Que sean deterministas es lo que hace seguros los reintentos y
el re-seeding: reenviar el mismo movimiento tras una caída se reporta como «ya
aplicado» en vez de mover el dinero dos veces.

### `seccomp=unconfined`, y en qué servicios

TigerBeetle usa `io_uring`, y el **perfil seccomp por defecto de Docker bloquea esas
syscalls**. Sin relajarlo, el formateo aborta con
`io_uring is not available: PermissionDenied`.

Lo necesitan **tres de los cuatro** servicios que hablan con el ledger: la réplica,
la API y el seeder — porque el cliente Go **también** abre su propio `io_uring`, no
solo el servidor. El único contenedor que conserva el perfil por defecto es `web`,
que es además el único expuesto a internet.

### Alpine no es una opción para la API

El cliente Go de TigerBeetle solo distribuye librerías nativas para **glibc**
(`aarch64-linux`, `x86_64-linux`) — no hay variante musl. Compilar sobre Alpine falla
con todos los tipos del paquete `undefined`, porque las build constraints excluyen el
paquete entero. La imagen de la API se basa en Debian y necesita `CGO_ENABLED=1`.

---

## Estructura

```
api/                      backend Go (module github.com/JuanKsPty/corebank/api)
  cmd/
    api/                  el servicio: wiring, migraciones al arrancar, apagado ordenado
    seed/                 importador del dataset, idempotente
    tbsmoke/              valida el modelo contable contra TigerBeetle real
  internal/
    money/                centavos enteros, parseo exacto desde texto
    ledger/               dominio del dinero y contrato del backend
    tigerbeetle/          adaptador del cliente
    store/                Postgres con pgx, migraciones embebidas
    auth/                 hash, JWT, refresh tokens, middleware
    identity/             el usuario del request; sin setter alcanzable desde un body
    accounts/             cuentas, saldos, apertura
    transactions/         movimientos, doble escritura, confirmaciones, sweeper
    mcpserver/            servidor MCP y las 6 herramientas
    llm/                  proveedor: Anthropic o deshabilitado
    chat/                 loop agéntico y fallback de reglas
    server/ httpx/        router, middleware, errores, respuestas
    config/ logging/      configuración por entorno y logs estructurados
    seeder/               la importación en sí
  migrations/             SQL de creación de tablas
  seed/data/              dataset de prueba (commiteado a propósito)
web/                      frontend Vite + React + TypeScript
  src/api/                cliente fetch, tipos escritos a mano, SSE
  src/components/         shell (dos experiencias), assistant, statement, ui (shadcn)
  src/pages/              landing, acceso, registro, panel, cuentas, mover, historial
  src/lib/                 formato, validación, layout, navegación, queries
deploy/                   topología de despliegue
docker-compose.yml        el sistema completo, para un clon limpio
docker-compose.dev.yml    solo las bases de datos, para desarrollar
```

---

## Desarrollo

```sh
docker compose -f docker-compose.dev.yml up -d

cd api && go test ./...
go run ./cmd/tbsmoke      # valida el modelo contable contra TigerBeetle
go run ./cmd/api

cd web && npm install && npm run dev
```

Los tests de integración de la capa de dinero necesitan TigerBeetle corriendo. Si no
lo encuentran **se saltan en lugar de fallar**, así que un checkout sin
infraestructura sigue dando `go test ./...` en verde.

### En macOS: flag del linker obligatorio

El cliente Go de TigerBeetle enlaza un archivo estático cuyos miembros no están
alineados a 8 bytes, y el linker actual de Apple lo rechaza:

```
ld: 64-bit mach-o member 'libtb_client.a.o' not 8-byte aligned
```

El linker clásico sí lo acepta, así que **cualquier** comando de Go que enlace el
cliente necesita:

```sh
export CGO_LDFLAGS="-Wl,-ld_classic"
```

No se puede declarar en el código con `#cgo LDFLAGS`: Go mantiene una lista blanca de
flags y este no está en ella. Solo afecta al desarrollo local — las imágenes compilan
sobre Linux.

---

## Cómo comprobar que funciona

```sh
curl -s localhost:8080/healthz          # postgres y tigerbeetle
```

1. Entra con `ihernandez@email.com` / `Isabel2024!`. El saldo debe ser **exactamente
   32,354.53**, igual que en el JSON de origen.
2. Ingresa $100 → 32,454.53. Retira $50 → 32,404.53.
3. Retira **$1,000,000** → error de fondos insuficientes, **y el saldo intacto**.
4. Transfiere a otra cuenta del fixture: el origen baja y el destino sube lo mismo.
5. Pídele al asistente «Retira $250». Mira **el segmento retenido aparecer en la barra
   de saldo** antes de confirmar. Cancela → el saldo vuelve. Repite y confirma → el
   movimiento aparece en el historial marcado como hecho por el asistente.
6. Prepara otro movimiento y **no respondas dos minutos**: la reserva se libera sola.
7. `docker compose down && docker compose up` → **no** vuelve a sembrar y los saldos
   persisten.

---

## Despliegue

El demo corre en cuatro piezas, y cada frontera existe por un motivo:

| Pieza | Por qué está separada |
|---|---|
| **PostgreSQL** | Recurso gestionado. La API la alcanza por nombre DNS: pgx resuelve hostnames. |
| **TigerBeetle** | Su data file se formatea **exactamente una vez** y hacerlo dos veces destruye el ledger, así que queda fuera del alcance de un redespliegue de la app. |
| **API + seeder** | Comparten ciclo de vida: el seeder corre después de que la API migre. Necesitan `seccomp=unconfined`. |
| **web** | No necesita ningún privilegio. **El único contenedor expuesto a internet es el único con el perfil de seguridad por defecto.** |

La API y el seeder van como Compose y no como servicios de Swarm porque **Swarm
ignora `security_opt`**, y sin él el cliente del ledger aborta al iniciar. El
frontend sí es un servicio de Swarm, porque no necesita nada de eso.

`deploy/api.compose.yml` describe la pieza de la API tal como se despliega.
