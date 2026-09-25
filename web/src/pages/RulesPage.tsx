import { useState } from 'react'
import { PencilIcon, Trash2Icon } from 'lucide-react'

import { ApiError } from '@/api/client'
import type { CategoryRule } from '@/api/types'
import { categoryNames } from '@/components/EntryList'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { useCategories, useDeleteRule, useRules, useSaveRule } from '@/lib/queries'

interface Draft {
  id?: string
  match_text: string
  category_id: string
  transfer: boolean
  apply_to_existing: boolean
}

const EMPTY: Draft = {
  match_text: '',
  category_id: '',
  transfer: false,
  apply_to_existing: true,
}

/**
 * The owner's rules: "movements whose text contains X go to category Y", or
 * "count as a transfer between my accounts". A rule never overwrites a category
 * the owner or the assistant chose by hand.
 */
export function RulesPage() {
  const rules = useRules()
  const categories = useCategories()
  const save = useSaveRule()
  const remove = useDeleteRule()
  const [draft, setDraft] = useState<Draft>(EMPTY)
  const [applied, setApplied] = useState<number | null>(null)

  const names = categoryNames(categories.data?.categories)
  const options = [...names.entries()].sort((a, b) => a[1].localeCompare(b[1], 'es'))

  function submit(event: React.FormEvent) {
    event.preventDefault()
    setApplied(null)
    save.mutate(
      {
        id: draft.id,
        input: {
          match_text: draft.match_text.trim(),
          category_id: draft.category_id || null,
          transfer: draft.transfer,
          apply_to_existing: draft.apply_to_existing,
        },
      },
      {
        onSuccess: (rule) => {
          setApplied(rule.applied ?? 0)
          setDraft(EMPTY)
        },
      },
    )
  }

  function edit(rule: CategoryRule) {
    setApplied(null)
    setDraft({
      id: rule.id,
      match_text: rule.match_text,
      category_id: rule.category_id ?? '',
      transfer: rule.transfer,
      apply_to_existing: true,
    })
  }

  return (
    <div className="mx-auto max-w-3xl space-y-5">
      <header className="hidden md:block">
        <p className="type-eyebrow">Reglas</p>
        <h1 className="type-display mt-1.5 text-[1.75rem]">
          Cómo se clasifican tus movimientos
        </h1>
      </header>

      <form className="card space-y-3 p-4" onSubmit={submit}>
        <h2 className="type-eyebrow">{draft.id ? 'Editar regla' : 'Nueva regla'}</h2>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="regla-texto">Si el concepto contiene</FieldLabel>
            <Input
              id="regla-texto"
              value={draft.match_text}
              onChange={(e) => setDraft({ ...draft, match_text: e.target.value })}
              placeholder="Ej. SUPER 99"
              autoComplete="off"
              required
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="regla-categoria">Categoría</FieldLabel>
            <NativeSelect
              id="regla-categoria"
              className="w-full"
              value={draft.category_id}
              onChange={(e) => setDraft({ ...draft, category_id: e.target.value })}
            >
              <NativeSelectOption value="">Ninguna</NativeSelectOption>
              {options.map(([id, name]) => (
                <NativeSelectOption key={id} value={id}>
                  {name}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>
        </div>
        <label className="flex items-center gap-2 text-[0.875rem]">
          <input
            type="checkbox"
            checked={draft.transfer}
            onChange={(e) => setDraft({ ...draft, transfer: e.target.checked })}
          />
          Es una transferencia entre mis cuentas (no cuenta como gasto ni ingreso)
        </label>
        <label className="flex items-center gap-2 text-[0.875rem]">
          <input
            type="checkbox"
            checked={draft.apply_to_existing}
            onChange={(e) => setDraft({ ...draft, apply_to_existing: e.target.checked })}
          />
          Aplicarla también a los movimientos que ya tengo
        </label>
        <div className="flex gap-2">
          <Button type="submit" disabled={save.isPending}>
            {draft.id ? 'Guardar' : 'Crear regla'}
          </Button>
          {draft.id && (
            <Button type="button" variant="outline" onClick={() => setDraft(EMPTY)}>
              Cancelar
            </Button>
          )}
        </div>
        {save.error && (
          <p className="text-[0.8125rem] text-destructive">
            {save.error instanceof ApiError ? save.error.message : 'No se pudo guardar.'}
          </p>
        )}
        {applied !== null && (
          <p className="text-[0.8125rem] text-ink-soft" role="status">
            Guardada.{' '}
            {applied === 1
              ? 'Se aplicó a 1 movimiento.'
              : `Se aplicó a ${applied} movimientos.`}
          </p>
        )}
      </form>

      {rules.isError ? (
        <Alert variant="destructive">
          <AlertTitle>No pudimos cargar tus reglas</AlertTitle>
          <AlertDescription>Vuelve a intentarlo en un momento.</AlertDescription>
        </Alert>
      ) : (
        <section className="card">
          {rules.isLoading ? (
            <div className="space-y-2 p-4">
              <Skeleton className="h-3 w-full" />
              <Skeleton className="h-3 w-2/3" />
            </div>
          ) : (rules.data?.rules ?? []).length === 0 ? (
            <p className="p-4 text-[0.875rem] text-ink-soft">
              Aún no tienes reglas. Crea una arriba y tus próximos imports la usarán.
            </p>
          ) : (
            <ul className="divide-y divide-rule">
              {rules.data?.rules.map((rule) => (
                <li
                  key={rule.id}
                  className="flex items-center justify-between gap-3 px-4 py-3"
                >
                  <div className="min-w-0 text-[0.875rem]">
                    <p className="truncate font-medium">“{rule.match_text}”</p>
                    <p className="truncate text-ink-soft">
                      {[
                        rule.category_id
                          ? (names.get(rule.category_id) ?? 'Categoría')
                          : null,
                        rule.transfer ? 'Transferencia' : null,
                      ]
                        .filter(Boolean)
                        .join(' · ')}
                    </p>
                  </div>
                  <div className="flex shrink-0 gap-1">
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label="Editar"
                      onClick={() => edit(rule)}
                    >
                      <PencilIcon aria-hidden="true" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label="Borrar"
                      disabled={remove.isPending}
                      onClick={() => remove.mutate(rule.id)}
                    >
                      <Trash2Icon aria-hidden="true" />
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
    </div>
  )
}
