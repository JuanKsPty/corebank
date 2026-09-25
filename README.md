# corebank

Una app de finanzas personales que lee lo que tus bancos ya imprimieron —estados de
cuenta de **Banco General** y **BAC**, la tarjeta de crédito de Banco General y la
cuenta de **Interactive Brokers**— y te dice cuánto tienes, cuánto debes, en qué
gastas y, sobre todo, **dónde deja de cuadrar una cuenta con su banco**.

Backend en Go sobre PostgreSQL, interfaz en React y un asistente de IA que analiza tus
datos a través del [Model Context Protocol](https://modelcontextprotocol.io).

**En vivo:** <https://corebank.juank.tech>

---

## Levantarlo

```sh
git clone https://github.com/JuanKsPty/corebank.git
cd corebank
cp .env.example .env
docker compose up
```

La interfaz queda en <http://localhost:5173> y la API en <http://localhost:8080>. Cada
valor del `.env` tiene un default que funciona, incluida la clave de IA, que puede
quedar vacía. El stack completo cabe en unos 450 MiB: API 160m, web 32m, PostgreSQL 256m.

Para desarrollar, solo la base de datos:

```sh
docker compose -f docker-compose.dev.yml up -d
cd api && go run ./cmd/api
cd web && npm install && npm run dev
```

---

## El modelo: un espejo de lo que imprimió el banco

La verdad sobre tu dinero la tiene el banco, no esta app. Así que corebank no lleva una
contabilidad propia: guarda **una fila por cada línea que imprimió un estado de
cuenta**, con el saldo que el banco imprimió al lado, y todo lo demás se calcula al
leer.

| Tabla | Qué guarda |
|---|---|
| `accounts` | Una por cuenta real, identificada como la identifica su banco: institución, número y moneda. En BAC el producto, no el número de cliente. |
| `entries` | Una por línea impresa. Monto con signo desde tu punto de vista: un cargo de tarjeta es negativo, y una tarjeta que debe $14.30 tiene saldo −14.30. Fecha civil de Panamá, sin zona horaria que la corra un día. |
| `statements` | Período y saldos del estado de cuenta: inicial, final, disponible, retenido. |
| `balance_checkpoints` | «Al cierre de este día el saldo era X», del estado de cuenta, del Cash Report de IBKR o ingresado a mano. Uno por cuenta es el **ancla**. |
| `import_runs` | Cada import o sincronización con lo que vio: líneas nuevas, duplicadas, fallidas, advertencias. |

**Saldo** = saldo inicial (derivado del ancla) + la suma de los movimientos.
**Descuadre** = saldo declarado − saldo calculado. Cero significa que la cuenta dice
exactamente lo mismo que su banco.

Un trigger impide cambiar el monto, las fechas o la cuenta de una línea importada. Lo
que sí puedes cambiar —categoría, nota, si es una transferencia— no altera ningún saldo.

### Por qué no TigerBeetle

corebank empezó sobre un ledger de doble entrada en TigerBeetle. Para una app que
**refleja** saldos que ya existen en otro lado, esa regla de no sobregiro inventaba
saldos iniciales, perdía líneas que el ledger rechazaba y dejaba las tarjetas fuera del
patrimonio. [La decisión completa está en el ADR](docs/adr/0001-postgres-only.md).

### Conciliación

`GET /api/accounts/{id}/reconciliation` recorre la cuenta en el orden del banco y, en
cada línea, pone el saldo calculado junto al que imprimió el banco. La pantalla de la
cuenta se abre en **la primera línea donde la diferencia cambia**: si un import perdió
una fila, esa es la fila siguiente. También marca los huecos entre estados de cuenta,
que son movimientos que nunca se importaron.

### Transferencias entre tus cuentas

Pagar la tarjeta desde tu cuenta no es gastar dos veces. La fila «Pagos» de la tarjeta
y los depósitos o retiros de IBKR se importan como `transfer`. Si hay **exactamente
una** línea bancaria del monto opuesto en ±5 días, el par se confirma solo al importar.
Si hay dudas, el panel te lo sugiere para que lo confirmes o lo descartes, y un par
descartado no vuelve a aparecer.

Las transferencias y la compraventa de valores **nunca** cuentan como gasto ni ingreso.
Gasto es gasto + comisiones + intereses − reembolsos, e ingreso es ingreso, con el
mismo filtro para el historial, el CSV, los reportes y el asistente.

### Reglas

«Lo que contenga SUPER 99 va a Supermercado.» Una regla se aplica a cada import y,
si quieres, a lo que ya importaste, pero **nunca pisa una categoría que elegiste a
mano**.

---

## Imports

| Fuente | Archivo | Qué se lee |
|---|---|---|
| Banco General, cuenta | `.xlsx` | Movimientos, saldo corrido por línea y «Saldo total». |
| BAC, cuenta | `.csv` | Producto, Saldo Inicial, en Libros, Disponible y el Resumen como control. |
| Banco General, tarjeta | `.txt` | Movimientos y Fecha proceso. No trae saldos: la tarjeta queda **sin saldo inicial** hasta que ingresas el adeudado al corte de un estado en PDF. |
| IBKR | Flex Web Service | Cash Transactions, Trades (Execution), Open Positions y Cash Report. |

- **Reimportar es seguro.** La clave de dedup sale solo de la identidad externa de la
  línea, nunca de un id interno, así que el mismo archivo no crea filas nuevas y dos
  archivos que se solapan se deduplican.
- **Cada archivo va en una transacción.** O entra entero o no entra.
- **Una cadena de saldos rota es una advertencia, no un rechazo.** Solo se rechaza lo
  que no se puede leer.
- **Dry-run.** `POST /api/imports/dry-run` lee el archivo y devuelve lo que entendió
  —cuenta, período, saldos, primer quiebre— sin escribir nada.

### IBKR

El cliente no envía fechas: IBKR devuelve el *Period* guardado en el Flex Query. Para
que la sincronización traiga datos nuevos, configura el query con **Period = Last 365
Calendar Days** y las secciones Open Positions (Summary), Trades (Execution), Cash
Transactions (Detail) y **Cash Report**. IBKR publica un día después de su proceso
nocturno, así que sincroniza por la mañana. Cada sync muestra el período que devolvió
IBKR y la fecha en que lo generó, y advierte si vino viejo o le falta una sección.

```sh
docker compose --profile ibkrsync run --rm ibkrsync
```

Es un job de una sola pasada, pensado para programarse una vez al día por la mañana.

---

## El asistente

Un **servidor MCP que corre dentro del mismo binario**, conectado al cliente por un
transporte en memoria. Las herramientas llaman a los mismos servicios que los handlers
REST.

| Lee | Propone |
|---|---|
| `list_accounts`, `list_movements`, `list_categories`, `list_rules` | `propose_recategorize` |
| `spending_by_category`, `cash_flow` | `propose_note`, `propose_rule` |
| `explain_drift`, `list_transfer_suggestions` | `propose_transfer_decision` |
| `get_portfolio` | `propose_checkpoint` |

**Ninguna herramienta escribe.** Una herramienta `propose_*` comprueba el cambio contra
tus datos y lo guarda como propuesta, con un resumen que escribe el servidor y no el
modelo. El chat la muestra como una tarjeta y **solo tu «Aplicar» hace el cambio**. Al
aplicar se vuelve a comprobar, y un `UPDATE` condicional hace que dos clics la apliquen
una sola vez. Nada de lo que puede proponer cambia un saldo, y ninguna herramienta mueve
dinero.

**Ninguna herramienta acepta un `user_id`.** La identidad viene del token de la petición
y queda fijada en el servidor MCP antes de que el modelo diga nada, así que no hay
argumento que falsificar. Las descripciones de los movimientos entran al contexto del
modelo, y cada resultado que las trae le recuerda que son datos, no instrucciones.

### El gasto tiene techo

El registro está abierto, así que la clave de API es alcanzable por cualquiera que se
registre. Cada llamada reserva su costo estimado antes de hacerse, en una sola
sentencia:

```sql
UPDATE ai_budget SET reserved_micros = reserved_micros + $3
 WHERE scope = $1 AND key = $2
   AND spent_micros + reserved_micros + $3 <= cap_micros
```

Si no afecta ninguna fila, la llamada no se hace. Hay tres techos: el total, el diario y
el diario por usuario. Gana el más estrecho. Sin clave o sin presupuesto, el asistente
dice que no está disponible y por qué. Todo lo demás sigue funcionando.

---

## Variables de entorno

Todo tiene un default que funciona; ver `.env.example`.

| Variable | Default | Para qué |
|---|---|---|
| `DATABASE_URL` | `postgres://corebank:corebank@localhost:5432/corebank?sslmode=disable` | PostgreSQL. |
| `JWT_SECRET` | *(vacío)* | Firma los tokens. Vacío, se genera uno por proceso y las sesiones no sobreviven un reinicio. |
| `COREBANK_CONFIRM_WIPE` | *(vacío)* | La migración 00011 reemplaza el modelo de dinero y borra los datos financieros anteriores. La API no arranca sobre una base anterior a ella si esto no vale `00011`. |
| `ANTHROPIC_API_KEY` | *(vacío)* | Opcional. Nunca en el repo: CI falla si aparece una clave en un archivo versionado. |
| `ANTHROPIC_MODEL` | `claude-sonnet-5` | |
| `AI_BUDGET_USD` / `AI_DAILY_BUDGET_USD` / `AI_USER_DAILY_BUDGET_USD` | `4.00` / `1.00` / `0.30` | Techos de gasto del asistente. |
| `AI_MAX_TOOL_TURNS` | `4` | Vueltas de herramientas por mensaje. |
| `IBKR_TOKEN_ENCRYPTION_KEY` | *(vacío)* | 32 bytes en base64 (`openssl rand -base64 32`). Cifra el token de IBKR. Vacío, vincular IBKR se rechaza. Cambiarlo invalida el token guardado. |
| `ENV` / `LOG_LEVEL` | `development` / `info` | `production` pasa los logs a JSON. |

---

## Estructura

```
api/
  cmd/api                 la API
  cmd/import              importa un archivo a mano contra la base
  cmd/ibkrsync            sincroniza todas las cuentas de IBKR, una pasada
  internal/statement      parsers puros de BG, BAC y la tarjeta
  internal/ibkr           cliente y parser del Flex Web Service
  internal/imports        un archivo → una transacción
  internal/accounts       saldos, anclas, conciliación
  internal/movements      historial, categoría, nota, CSV
  internal/reports        gasto, ingreso y flujo
  internal/transfers      pares de transferencias entre tus cuentas
  internal/rules          reglas de categoría
  internal/proposals      lo que propone el asistente
  internal/mcpserver      las herramientas del asistente
  internal/chat           el loop del asistente y su techo de gasto
  migrations/             goose, embebidas en el binario
web/                      Vite + React + TypeScript
deploy/                   la API tal como se despliega
docs/adr/                 decisiones de arquitectura
```

---

## Desarrollo y CI

```sh
docker compose -f docker-compose.dev.yml up -d
cd api && TEST_DATABASE_URL='postgres://corebank:corebank@localhost:5432/corebank?sslmode=disable' go test ./...
cd web && npm run build
```

Cada test de integración crea su propia base desechable a partir de
`TEST_DATABASE_URL`. Sin ella esos tests se saltan en local. En CI (`CI=true`) fallan,
así que una base que falta nunca pasa como verde.

`.github/workflows/ci.yml` corre en cada pull request, incluidos los de una pila de PRs:

| Trabajo | Qué comprueba |
|---|---|
| `api` | `gofmt`, `go vet`, `go mod tidy -diff`, build estático y `go test -race` contra PostgreSQL |
| `web` | formato, typecheck y build |
| `secrets` | que no haya claves ni `.env` versionados |
| `images` | que los dos Dockerfiles construyan |

La API compila sin cgo (`CGO_ENABLED=0`). cgo solo se usa para el detector de carreras.

**No hay trabajo de deploy.** Dokploy despliega en cada push a `main`. El control está
en el merge, con una branch protection que exige estos checks.
