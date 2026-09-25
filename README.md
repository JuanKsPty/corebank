# corebank

Sistema de banca en línea con **ledger contable de doble entrada** sobre
[TigerBeetle](https://tigerbeetle.com), backend en Go y asistente de IA que opera
las cuentas en lenguaje natural a través del
[Model Context Protocol](https://modelcontextprotocol.io).

**En vivo:** <https://corebank.juank.tech> — crea una cuenta desde la portada.

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

> **Memoria de Docker: 4 GiB mínimo, 6 GiB recomendado.** TigerBeetle reserva
> ~2,3 GiB al arrancar independientemente de `--cache-grid`, y con Postgres al
> lado un Docker limitado a 4 GiB va justo.

### Solo las bases de datos, para desarrollar

```sh
docker compose -f docker-compose.dev.yml up -d
```

Levanta únicamente PostgreSQL y TigerBeetle, para correr la API con `go run` y la
interfaz con `npm run dev`. Comparte los volúmenes con el stack completo, así que
los datos son los mismos en ambos.

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

### Una cuenta se puede llamar como quiera su dueño

Un número identifica una cuenta; no ayuda a nadie a reconocerla. Sin alias, todas las
etiquetas salían del tipo —«Ahorros», «Corriente», «Inversión»— y **dos cuentas del
mismo tipo eran indistinguibles** salvo por los últimos cuatro dígitos. No es un caso
raro: un cliente puede tener seis cuentas y solo hay tres tipos.

El alias es opcional y va en PostgreSQL, nunca en el ledger, que no guarda texto. Si
está vacío la interfaz muestra el tipo, y ese respaldo **existe en una sola función**
(`accountLabel`) porque seis pantallas etiquetan cuentas y la que se olvidara del
respaldo sería justo la que enseñara dos cuentas idénticas.

Dos detalles que no son cosméticos. El límite de 40 se mide en **caracteres y no en
bytes**: «Ahorros de mamá» son 15 caracteres y 16 bytes, y un límite en bytes recorta
los nombres con tilde antes de tiempo. Y se rechazan los caracteres invisibles y los de
control bidireccional: un alias hecho de espacios de ancho cero se ve en blanco sin
estar vacío, y `U+202E` invierte cómo se dibuja el texto que le sigue. Una etiqueta de
cuenta se lee para decidir a dónde va el dinero, así que no puede mostrar algo distinto
de lo que guarda.

Los alias **no son únicos**, como en un banco real. El número sigue siendo el
identificador y la interfaz lo muestra siempre al lado.

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
La interfaz dice cuál motor está activo, porque presentar un fallback de reglas como
IA sería mentir sobre el producto.

### El gasto también es imposible por construcción

El despliegue es público y el registro está abierto. Eso significa que la clave de
API es alcanzable por cualquiera que se registre, y que sin un techo el presupuesto es lo que decida gastar un
desconocido. Un límite por IP lo frena; no lo acota.

Así que **el gasto usa el mismo mecanismo que el dinero**. Una transferencia reserva
fondos antes de moverlos, porque consultar un saldo y luego debitarlo son dos
sentencias y veinte peticiones simultáneas leen todas la misma cifra alentadora. Una
llamada al modelo reserva su costo estimado antes de hacerse, por la misma razón, y
liquida la cifra real después. El techo aguanta con concurrencia porque la
comprobación y el incremento son **una sola sentencia** — la misma forma que el
`debits_must_not_exceed_credits` del ledger:

```sql
UPDATE ai_budget SET reserved_micros = reserved_micros + $3
 WHERE scope = $1 AND key = $2
   AND spent_micros + reserved_micros + $3 <= cap_micros
```

Cero filas afectadas significa que el techo se cruzaría, y entonces la llamada no se
hace. Tres techos, y el más estrecho gana: total, diario y por cliente y día. Todo en
**micro-dólares enteros**, porque una llamada cuesta una fracción de centavo y un
contador que no puede representar lo que cuenta no es un contador.

Lo que esto garantiza, con precisión: una llamada no empieza si su *estimación* no
cabe, y la estimación es deliberadamente alta — cobra todo token de entrada a tarifa
sin caché y asume que el modelo escribe hasta su límite de salida, y ninguna de las
dos cosas suele ser cierta. Liquidar devuelve la diferencia no gastada, que es por lo
que caben más llamadas de las que la estimación predecía. Eso es el mecanismo
funcionando, no una fuga.

**Y cuando se agota, se degrada en vez de romperse.** Antes el proveedor se elegía una
sola vez al arrancar: un 400 de «credit balance too low» se convertía en «el asistente
no está disponible ahora mismo, vuelve a intentarlo en un momento», que es falso —no
va a volver nunca— mientras el motor de reglas estaba ahí sin usarse. Ahora un fallo
se clasifica por si va a resolverse solo:

| Situación | Trato |
|---|---|
| 400 que menciona crédito o facturación | **permanente**: se enclava, no se reintenta |
| 401 · 403 (clave rechazada o revocada) | **permanente** |
| 429 · 5xx · timeouts | **transitorio**: este mensaje lo responden las reglas, el siguiente reintenta |
| Techo de gasto alcanzado | permanente si es el total; el diario reabre mañana |

La interfaz tiene **cuatro** estados, no dos, porque `is_ai` solo colapsaba tres
situaciones distintas en un «sin IA» que no explica nada:

| Estado | Etiqueta |
|---|---|
| `ai` | `Modelo claude-sonnet-5` |
| `unconfigured` | `Sin IA configurada · respondo con reglas` |
| `budget_exhausted` | `Presupuesto de IA agotado · respondo con reglas` |
| `degraded` | `IA no disponible ahora · respondo con reglas` |

El evento `done` del SSE lleva el motor efectivo, así que la etiqueta cambia sin
recargar: el presupuesto se puede agotar a mitad de una sesión, y atribuir a un modelo
una respuesta que no escribió sería la misma mentira que presentar las reglas como IA.

El saldo restante se registra en el log y **no se publica**: decirle a un visitante
cuánto queda es decírselo también a quien quiera agotarlo.

### Qué cuesta, medido y no estimado

`ai_usage` guarda una fila por llamada con las **cuatro** cuentas de tokens
separadas —entrada, salida, escritura de caché, lectura de caché— porque se facturan
a cuatro tarifas distintas: escribir la caché cuesta un 25% más que enviar los tokens
en claro, leerla cuesta una décima parte. Una sola columna `input_tokens` cotizaría mal
toda llamada después de la primera y ocultaría si la caché sirve de algo.

El preámbulo fijo —prompt de sistema más los seis esquemas de herramientas— sale en
**cada** llamada, incluida cada vuelta del bucle de herramientas, lo que lo convierte
en lo más caro de una conversación y lo más rentable de cachear. Se mide, no se supone:

```bash
cd api && go test ./internal/chat/ -run TestFixedPreambleSize -v
```

Sin clave reporta los caracteres (**6.203**: 2.816 del prompt de sistema y 3.387 de
las herramientas). Con `ANTHROPIC_API_KEY` puesta pregunta el conteo exacto a
`/v1/messages/count_tokens`, que **no cobra nada**, y falla si el preámbulo baja de
los 1.024 tokens que la caché necesita para activarse en Sonnet. Esa es la regresión
que ese test existe para atrapar: por debajo de la línea, `cache_control` se sigue
enviando y deja de hacer nada, sin que nada falle para avisarlo.

### La caché, medida en producción

Un mensaje real deja dos líneas en el log, una por vuelta del bucle de herramientas:

```
AI spend  cost_micros=7269  cache_write_tokens=2722  cache_read_tokens=0
AI spend  cost_micros=1375  cache_write_tokens=103   cache_read_tokens=2722
```

La primera llamada escribe el preámbulo en la caché; la segunda **lo lee entero** y
cuesta una quinta parte. El mensaje completo sale por **8.644 micro-dólares, menos de
un centavo**, así que los $4 del techo dan para unos 460 mensajes.

Esa medición corrigió dos cosas que este README afirmaba y que eran falsas.

El preámbulo son **~2.800 tokens, no los 1.500–1.700** que se estimaban contando
caracteres. La proporción real es de **2,2 caracteres por token**, no los 3,5–4 de la
regla habitual, porque el español y los esquemas JSON tokenizan más denso que la prosa
inglesa de la que sale esa regla. Los 2.722 del log son de antes de añadirle una línea
al prompt.

Eso obligó a bajar `charsPerToken` de 3 a 2 en la estimación que reserva presupuesto.
Con 3, la parte de entrada quedaba un **27% por debajo** de la real, y la reserva solo
seguía siendo mayor que el gasto porque el techo de salida —cinco veces más caro y que
se asume gastado entero— la sostenía. Eso es la garantía del techo apoyada en una
constante que existe para otra cosa: bajar `maxReplyTokens` por un buen motivo la habría
roto en silencio. Hay un test que fija esa propiedad por separado.

Y con eso se cae el argumento de coste a favor de Sonnet. El preámbulo supera **los dos
mínimos** —1.024 de Sonnet y 2.048 de Haiku 4.5— así que en Haiku también se cachearía;
decir lo contrario era una conclusión sacada de una cifra mal estimada. Sonnet se queda
por lo que sí se sostiene: es un bucle agéntico con un contrato de confirmación que un
modelo más pequeño se salta, y a menos de un centavo por mensaje el ahorro no compra
nada que valga ese riesgo.

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
| `POST` | `/api/accounts` | Abre una cuenta adicional. Máximo 6 por cliente. Acepta un `alias` opcional. |
| `GET` | `/api/accounts/{number}` | Detalle. |
| `PATCH` | `/api/accounts/{number}` | Cambia el alias. Vacío lo quita. |
| `GET` | `/api/accounts/{number}/balance` | Saldo puntual. |
| `GET` | `/api/accounts/{number}/transactions` | Historial de la cuenta, paginado por cursor. |
| `GET` | `/api/transactions` | Historial consolidado, con filtros y paginación por cursor. |
| `GET` | `/api/transactions/export.csv` | Los mismos filtros, como archivo. Sin paginar: un extracto es un documento. |
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

### El extracto en CSV

`export.csv` toma **los mismos filtros** que el historial y los aplica igual, así que
el archivo describe exactamente lo que había en pantalla al pulsar el botón. Lo único
que ignora es el cursor: un extracto es un documento, no una página de uno, así que
recorre las páginas por dentro y las escribe como un solo archivo, **en streaming**
—el coste en memoria no lo fija el cliente con más movimientos del banco.

Tres detalles que deciden si el archivo sirve:

- **Lleva BOM de UTF-8.** Excel lee un CSV con la codificación heredada del sistema si
  no lo encuentra, y convierte cada «Depósito» en galimatías. Todo lo que no es Excel
  lo ignora.
- **Los montos van con punto y dos decimales**, la misma forma que devuelve la API.
  Un «1.234,56» localizado se leería mejor y dejaría de ser un dato que se pueda
  volver a parsear.
- **Las fechas en RFC 3339 y en UTC**, para que la columna signifique lo mismo la abra
  quien la abra y el archivo no se lleve la zona horaria del servidor.

Una descripción con comas, comillas o saltos de línea no descuadra las columnas: hay
un test que escribe esas cuatro y vuelve a leer el archivo para comprobarlo.

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
| `ANTHROPIC_API_KEY` | *(vacío)* | Única dependencia externa, y opcional. Vacía, el chat responde con reglas locales. **Nunca en el repo**: va en las variables del despliegue, y un check de CI falla si aparece una clave en un archivo versionado. |
| `ANTHROPIC_MODEL` | `claude-sonnet-5` | |
| `CONFIRMATION_TTL` | `2m` | Cuánto retiene una reserva antes de que TigerBeetle la libere. |
| `AI_MAX_TOOL_TURNS` | `4` | Vueltas de herramientas por mensaje. Una pregunta bancaria necesita una o dos; cada vuelta reenvía la conversación, así que esto es un techo de costo tanto como de seguridad. |
| `AI_BUDGET_USD` | `4.00` | Techo de gasto de por vida. `0.00` deshabilita el gasto por completo, que es un estado soportado: responden las reglas. Solo hasta el centavo — pasa por el mismo parser que el dinero, que rechaza más de dos decimales. |
| `AI_DAILY_BUDGET_USD` | `1.00` | Para que un día malo no se lleve el presupuesto entero. El diario reabre al día siguiente sin reiniciar nada. |
| `AI_USER_DAILY_BUDGET_USD` | `0.30` | Para que una cuenta no consuma la parte de todos los demás. |
| `ENV` | `development` | `production` pasa los logs a JSON. |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` | |
| `LOGIN_RATE_LIMIT` | `10` | Intentos de login por ventana. |
| `BCRYPT_COST` | | Coste del hash de contraseñas. |
| `ACCESS_TOKEN_TTL` / `REFRESH_TOKEN_TTL` | | Vida de los tokens. |
| `CORS_ORIGINS` | `http://localhost:5173` | Solo para `npm run dev`: en el stack de contenedores nginx sirve la SPA y proxea `/api` en el mismo origen, así que no hay CORS. |
| `DB_MAX_CONNS` / `DB_CONNECT_WAIT` / `TB_CONNECT_WAIT` | | Pool y esperas de arranque. |
| `HTTP_SHUTDOWN_TIMEOUT` | | Margen del apagado ordenado. |

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
TigerBeetle necesita. Que sean deterministas es lo que hace seguros los reintentos:
reenviar el mismo movimiento tras una caída se reporta como «ya
aplicado» en vez de mover el dinero dos veces.

### `seccomp=unconfined`, y en qué servicios

TigerBeetle usa `io_uring`, y el **perfil seccomp por defecto de Docker bloquea esas
syscalls**. Sin relajarlo, el formateo aborta con
`io_uring is not available: PermissionDenied`.

Lo necesitan **tres de los cuatro** servicios que hablan con el ledger: la réplica,
la API y los jobs de un solo uso — porque el cliente Go **también** abre su propio `io_uring`, no
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
    llm/                  proveedor, tarifas por modelo y cortes de caché
    chat/                 loop agéntico, fallback de reglas y techo de gasto
    server/ httpx/        router, middleware, errores, respuestas
    config/ logging/      configuración por entorno y logs estructurados
  migrations/             SQL de creación de tablas
web/                      frontend Vite + React + TypeScript
  src/api/                cliente fetch, tipos escritos a mano, SSE
  src/components/         shell (dos experiencias), assistant, statement, ui (shadcn)
  src/pages/              landing, acceso, registro, panel, cuentas, mover, historial
  src/lib/                 formato, validación, layout, navegación, queries
deploy/                   topología de despliegue
.github/workflows/        los checks que exige la protección de `main`
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

### El techo de gasto

Levanta el stack con `AI_BUDGET_USD=0.00` y una clave puesta. El asistente responde
igual, la etiqueta dice **«Presupuesto de IA agotado · respondo con reglas»** y en el
log aparece `the lifetime AI budget is exhausted`. Comprueba que la API **no se llamó
ni una vez**: el techo se evalúa antes de la llamada, no después.

Con una clave inválida (`ANTHROPIC_API_KEY=sk-ant-invalida`) el primer mensaje sí
intenta, recibe 401, se enclava y lo responden las reglas. El segundo mensaje **no
reintenta** — `grep 'failed permanently'` en el log aparece una sola vez. Y en la base:

```sql
SELECT scope, cap_micros, spent_micros, reserved_micros FROM ai_budget;
SELECT model, input_tokens, output_tokens, cache_write_tokens, cache_read_tokens,
       cost_micros FROM ai_usage ORDER BY id;
```

`spent_micros` en cero y `reserved_micros` en cero: una petición rechazada no consume
nada y la reserva se devuelve.

### Que la caché de verdad se está usando

Con una clave real, manda dos mensajes seguidos y mira el log del segundo:

```
INFO AI spend model=claude-sonnet-5 cost_micros=… cache_read_tokens=1700 …
```

`cache_read_tokens` en cero en el segundo mensaje significa que la caché no está
funcionando, y entonces la elección de Sonnet sobre Haiku no se sostiene. Los cortes de
caché en sí los cubre `go test ./internal/llm/ -run Cache`, que revisa el JSON que
saldría por el cable: un corte mal puesto no es un error —la petición funciona y solo
paga de más—, así que la única forma de notarlo es la factura o un test.

---

## Despliegue

El demo corre en cuatro piezas, y cada frontera existe por un motivo:

| Pieza | Por qué está separada |
|---|---|
| **PostgreSQL** | Recurso gestionado. La API la alcanza por nombre DNS: pgx resuelve hostnames. |
| **TigerBeetle** | Su data file se formatea **exactamente una vez** y hacerlo dos veces destruye el ledger, así que queda fuera del alcance de un redespliegue de la app. |
| **API** | Migra el esquema al arrancar. Necesita `seccomp=unconfined`. |
| **web** | No necesita ningún privilegio. **El único contenedor expuesto a internet es el único con el perfil de seguridad por defecto.** |

La API va como Compose y no como servicios de Swarm porque **Swarm
ignora `security_opt`**, y sin él el cliente del ledger aborta al iniciar. El
frontend sí es un servicio de Swarm, porque no necesita nada de eso.

`deploy/api.compose.yml` describe la pieza de la API tal como se despliega.

### La puerta va en el merge, no en el deploy

`.github/workflows/ci.yml` corre en cada pull request a `main` y en cada push a
`main`. Son cuatro trabajos en paralelo:

| Trabajo | Qué comprueba |
|---|---|
| `api` | `gofmt`, `go vet`, `go mod tidy -diff`, build y `go test -race ./...` |
| `web` | `npm ci`, formato, typecheck y build |
| `secrets` | Que no haya una clave `sk-ant-…` ni un `.env` en ningún archivo versionado |
| `images` | Que los dos Dockerfiles sigan construyendo, sin publicar nada |

**No hay trabajo de deploy, ni un token de Dokploy en los secretos de este
repositorio.** Dokploy está conectado por una GitHub App y despliega solo en un push a
`main`; la puerta vive en el *merge*, como una regla de branch protection que exige
estos checks. Así `main` solo puede contener commits que pasaron, y desplegar cada
commit de `main` es seguro **por construcción** en vez de porque un pipeline se acuerde
de comprobar primero — la misma forma de argumentar que el resto del proyecto. El
efecto secundario es una credencial menos que filtrar.

Nada de esto necesita una base de datos ni un ledger: el único test que quiere
TigerBeetle hace `t.Skip` cuando no lo alcanza, así que la suite entera corre en un
runner pelado. `ubuntu-latest` y no algo más ligero, porque el cliente de TigerBeetle
solo enlaza contra glibc.

`go test -race` no es decorativo: el techo de gasto del asistente se guarda con
atómicos y se alcanza desde peticiones concurrentes. Un techo que solo aguanta cuando
las peticiones llegan de una en una no es un techo, y su test lanza veinte goroutines
para decirlo.

**Pendiente, y por qué no está:** construir las imágenes en CI y publicarlas en un
registro, para que el artefacto probado sea el que corre y el VPS no necesite el
toolchain de Go ni RAM para compilar el cliente del ledger. Es la decisión más
defendible; hoy cada deploy recompila en el host, que está verificado y funciona.
