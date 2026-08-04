import { Suspense, lazy, useEffect, useId, useState } from 'react'
import type { Matcher } from 'react-day-picker'
import { CalendarIcon } from 'lucide-react'

import { Field, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from '@/components/ui/input-group'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Skeleton } from '@/components/ui/skeleton'

/**
 * The calendar, split out of the main bundle.
 *
 * `react-day-picker` and the `date-fns` locale it needs are 26 KB gzipped — more than a
 * tenth of the whole application — for a control that lives behind a button on one
 * screen. Loading it when the button is pressed keeps that off the first paint of every
 * page, and by the time the popover has finished opening it is already there.
 */
/**
 * The single-date props this field actually uses.
 *
 * Spelled out rather than derived from the component's own props, because `DayPicker`
 * types its modes as a discriminated union and `Omit<…, 'locale'>` collapses it — which
 * takes `selected` and `onSelect` with it, since those only exist on the single-date
 * branch.
 */
interface CalendarPickerProps {
  autoFocus?: boolean
  defaultMonth?: Date
  selected?: Date
  onSelect: (date: Date | undefined) => void
  disabled?: Matcher | Matcher[]
}

/**
 * The calendar, split out of the main bundle.
 *
 * `react-day-picker` and the `date-fns` locale it needs are 26 KB gzipped — more than a
 * tenth of the whole application — for a control that lives behind a button on one
 * screen. Loading it when the button is pressed keeps that off the first paint of every
 * page, and by the time the popover has finished opening it is already there.
 */
const LazyCalendar = lazy(async () => {
  const [{ Calendar }, { es }] = await Promise.all([
    import('@/components/ui/calendar'),
    import('date-fns/locale/es'),
  ])
  return {
    default: (props: CalendarPickerProps) => <Calendar mode="single" locale={es} {...props} />,
  }
})

/**
 * A date field that writes the date the way this application's readers write it, and
 * lets them pick it instead if they would rather.
 *
 * `<input type="date">` was here and had to go. Its placeholder and its typing order
 * come from the browser's locale, not the page's, so a Spanish-language bank rendered
 * "mm/dd/yyyy" for anybody whose browser was set to en-US — and a customer who reads
 * 03/08 as the third of August had been handed a control that means the eighth of
 * March. There is no HTML or CSS that changes it: the format is the user agent's.
 *
 * So the display is ours and the value stays machine-readable. What is typed is
 * dd/mm/aaaa, what leaves through `onChange` is `yyyy-mm-dd`, and the two are only
 * connected when the text spells a real date — the parent never sees a half-typed day
 * as a filter. The calendar behind the button is the same value from the other
 * direction, for a date somebody would rather find than remember.
 */
export function DateField({
  label,
  value,
  onChange,
  min,
  max,
  className,
}: {
  label: string
  /** ISO `yyyy-mm-dd`, or empty. */
  value: string
  /** Called with ISO `yyyy-mm-dd`, or empty when the field is cleared. */
  onChange: (iso: string) => void
  /** ISO bounds. They explain a rejected date, and they grey it out in the calendar. */
  min?: string
  max?: string
  className?: string
}) {
  const id = useId()
  const [text, setText] = useState(() => toDisplay(value))
  const [open, setOpen] = useState(false)

  // Follow the value when it is changed from outside — the "Limpiar" button empties
  // every filter at once, and a field still showing what was typed into it would be
  // lying about what is being filtered.
  useEffect(() => {
    setText((current) => (toISO(current) === value ? current : toDisplay(value)))
  }, [value])

  const digits = text.replace(/\D/g, '')
  const iso = toISO(text)

  let problem: string | undefined
  if (digits.length === 8 && !iso) {
    problem = 'Esa fecha no existe.'
  } else if (iso && min && iso < min) {
    problem = 'Es anterior a la fecha inicial.'
  } else if (iso && max && iso > max) {
    problem = 'Es posterior a la fecha final.'
  }

  const handleText = (next: string) => {
    const formatted = format(next)
    setText(formatted)

    const parsed = toISO(formatted)
    // Emit only a complete, real date, or the empty string. Anything in between is
    // somebody mid-keystroke, and refetching the statement on each digit would be
    // three wrong requests on the way to the right one.
    if (parsed) onChange(parsed)
    else if (formatted === '') onChange('')
  }

  // Built as a list because a Matcher will not take an absent bound: with only one
  // field filled there is only one side to disable.
  const bounds: Matcher[] = []
  const lower = toDate(min)
  if (lower) bounds.push({ before: lower })
  const upper = toDate(max)
  if (upper) bounds.push({ after: upper })

  const handlePick = (date: Date | undefined) => {
    if (!date) return
    const picked = fromDate(date)
    setText(toDisplay(picked))
    onChange(picked)
    setOpen(false)
  }

  return (
    <Field data-invalid={problem ? true : undefined} className={className}>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <InputGroup>
        <InputGroupInput
          id={id}
          value={text}
          onChange={(event) => handleText(event.target.value)}
          placeholder="dd/mm/aaaa"
          // `numeric` rather than `decimal`: a date has no decimal point, and the
          // slashes are inserted for you.
          inputMode="numeric"
          autoComplete="off"
          maxLength={10}
          aria-invalid={problem ? true : undefined}
          aria-describedby={problem ? `${id}-error` : `${id}-hint`}
        />
        <InputGroupAddon align="inline-end">
          <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
              <InputGroupButton
                size="icon-xs"
                aria-label={`Elegir ${label.toLowerCase()} en el calendario`}
              >
                <CalendarIcon aria-hidden="true" />
              </InputGroupButton>
            </PopoverTrigger>
            <PopoverContent align="end" className="w-auto p-0">
              <Suspense fallback={<Skeleton className="m-2 h-56 w-60" />}>
                <LazyCalendar
                  autoFocus
                  defaultMonth={toDate(iso) ?? undefined}
                  selected={toDate(iso) ?? undefined}
                  onSelect={handlePick}
                  // The bounds are the other field's value, so a range cannot be
                  // inverted by picking rather than only by typing.
                  disabled={bounds.length > 0 ? bounds : undefined}
                />
              </Suspense>
            </PopoverContent>
          </Popover>
        </InputGroupAddon>
      </InputGroup>
      {problem ? (
        <FieldError id={`${id}-error`}>{problem}</FieldError>
      ) : (
        <FieldDescription id={`${id}-hint`}>Escríbela o elígela.</FieldDescription>
      )}
    </Field>
  )
}

/** Inserts the separators as the digits arrive, and refuses anything that is not one. */
function format(input: string): string {
  const digits = input.replace(/\D/g, '').slice(0, 8)
  if (digits.length <= 2) return digits
  if (digits.length <= 4) return `${digits.slice(0, 2)}/${digits.slice(2)}`
  return `${digits.slice(0, 2)}/${digits.slice(2, 4)}/${digits.slice(4)}`
}

/**
 * `dd/mm/aaaa` to `yyyy-mm-dd`, or empty when it is not yet a real date.
 *
 * The round trip through `Date` is what rejects the 31st of February: constructing it
 * rolls over into March, so comparing the parts back is the check. A regular expression
 * that only counted digits would accept it.
 */
function toISO(display: string): string {
  const digits = display.replace(/\D/g, '')
  if (digits.length !== 8) return ''

  const day = Number(digits.slice(0, 2))
  const month = Number(digits.slice(2, 4))
  const year = Number(digits.slice(4))
  if (month < 1 || month > 12 || day < 1) return ''

  const date = new Date(year, month - 1, day)
  if (
    date.getFullYear() !== year ||
    date.getMonth() !== month - 1 ||
    date.getDate() !== day
  ) {
    return ''
  }
  return `${String(year).padStart(4, '0')}-${digits.slice(2, 4)}-${digits.slice(0, 2)}`
}

/** `yyyy-mm-dd` to `dd/mm/aaaa`. */
function toDisplay(iso: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso)
  return match ? `${match[3]}/${match[2]}/${match[1]}` : ''
}

/**
 * `yyyy-mm-dd` to a `Date` at local midnight.
 *
 * Deliberately not `new Date(iso)`, which reads a bare date as UTC: at any negative
 * offset — Panama is UTC−5 — that lands on the previous evening, and a calendar built
 * from it highlights the day before the one the customer chose. The parts are passed
 * separately so the date means the same day it says.
 */
function toDate(iso: string | undefined): Date | null {
  if (!iso) return null
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso)
  if (!match) return null
  return new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
}

/** A `Date` from the calendar back to `yyyy-mm-dd`, read in local time for the same reason. */
function fromDate(date: Date): string {
  const year = String(date.getFullYear()).padStart(4, '0')
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}
