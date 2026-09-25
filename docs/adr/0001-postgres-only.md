# ADR 0001 — Solo PostgreSQL: el dinero como espejo de lo que imprimió el banco

- **Estado:** aceptada
- **Fecha:** 2026-09-24

## Contexto

corebank guardaba los saldos en TigerBeetle, un ledger de doble entrada, y el resto en
PostgreSQL. Las cuentas se creaban con `debits_must_not_exceed_credits`, y cada depósito
tenía como contrapartida una cuenta de patrimonio `world`.

Ese diseño sirve para un sistema que **origina** movimientos de dinero. corebank no
origina ninguno: todo movimiento sale de un estado de cuenta o de IBKR. Su trabajo es
**reflejar** saldos cuya verdad está en otro lado y decir dónde dejan de cuadrar. Con
el ledger:

- La regla de no sobregiro obligaba a **inventar un saldo inicial**: lo mínimo para que
  la primera línea no dejara la cuenta en negativo. Un estado de Banco General que
  termina en $107.50 quedaba en $20.00.
- Una línea que el ledger rechazaba ya estaba registrada como importada, así que **se
  perdía para siempre** y el siguiente import la contaba como duplicada.
- Una tarjeta es un pasivo. En un ledger de activos o su saldo tenía el signo al revés,
  o quedaba fuera del patrimonio.
- Depurar exigía cruzar dos almacenes que no comparten transacción.
- En operación costaba: ~2.3 GiB de RAM reservados en un VPS compartido,
  `seccomp=unconfined` en la API expuesta a internet, una IP fija, cgo con glibc y no
  poder usar Swarm.

La mayoría de los defectos de import venían del **modelo**: fechas, signos, identidad
de cuentas, dedup. Por eso migrar el mismo modelo a PostgreSQL habría dado los mismos
números malos.

## Decisión

Un modelo de **entrada simple** en PostgreSQL, sin ledger:

- `entries`: una fila por línea impresa, con el monto con signo desde el titular y el
  saldo que imprimió el banco. Un trigger impide cambiar lo importado.
- `balance_checkpoints`: saldos declarados («al cierre del día X era Y»). Uno por cuenta
  es el ancla, y el saldo inicial se **deriva** de él en vez de guardarse.
- El saldo y el descuadre se calculan al leer. No hay columna de saldo que pueda
  discrepar de los movimientos.
- Se eliminan los movimientos manuales (ingresar, retirar, transferir). El asistente
  solo propone cambios que no alteran saldos, y la persona los aplica.

Se consideró un diario de doble entrada sobre PostgreSQL. Se descartó porque añadía un
join a cada consulta y un 25–35 % más de código. Además, alejaba cada fila de la línea
del estado de cuenta que la originó, que es justo lo que hay que mirar al depurar un
descuadre.

## Consecuencias

- Depurar una cuenta es un `SELECT … ORDER BY balance_on` que se lee junto al PDF del
  banco, y la vista de conciliación señala la primera línea que no cuadra.
- La migración 00011 borra los datos financieros anteriores. Por eso exige
  `COREBANK_CONFIRM_WIPE=00011`, y todo se reimporta desde los archivos originales.
- La API compila estática, corre con el perfil de seguridad por defecto y cabe en
  160 MiB.
- Se pierde la garantía de no sobregiro del ledger, que aquí no protegía nada: el
  banco ya decidió si hubo sobregiro, y corebank solo lo refleja.
