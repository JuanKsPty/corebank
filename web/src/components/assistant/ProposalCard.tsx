import { ApiError } from '@/api/client'
import type { Proposal } from '@/api/types'
import { Button } from '@/components/ui/button'
import { useDecideProposal, useProposals } from '@/lib/queries'

const KIND_LABEL: Record<Proposal['kind'], string> = {
  recategorize: 'Categoría',
  note: 'Nota',
  rule: 'Regla',
  transfer_decision: 'Transferencia',
  checkpoint: 'Saldo del banco',
}

/**
 * The changes the assistant proposed and the owner has not decided.
 *
 * Read from the server rather than from the stream, so a proposal made before a
 * reload is still here to decide, and one decided in another tab disappears.
 */
export function PendingProposals() {
  const proposals = useProposals()
  const list = proposals.data?.proposals ?? []
  if (list.length === 0) return null

  return (
    <section aria-label="Propuestas del asistente" className="space-y-2">
      {list.map((proposal) => (
        <ProposalCard key={proposal.id} proposal={proposal} />
      ))}
    </section>
  )
}

function ProposalCard({ proposal }: { proposal: Proposal }) {
  const decide = useDecideProposal()

  return (
    <div className="rounded-[5px] border border-copper/40 bg-copper/5 px-3 py-2.5 text-[0.8125rem]">
      <p className="type-eyebrow text-copper">Propuesta · {KIND_LABEL[proposal.kind]}</p>
      <p className="mt-1 text-ink">{proposal.summary}</p>
      <div className="mt-2 flex gap-2">
        <Button
          size="sm"
          disabled={decide.isPending}
          onClick={() => decide.mutate({ id: proposal.id, apply: true })}
        >
          Aplicar
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={decide.isPending}
          onClick={() => decide.mutate({ id: proposal.id, apply: false })}
        >
          Descartar
        </Button>
      </div>
      {decide.error && (
        <p className="mt-1.5 text-danger-text">
          {decide.error instanceof ApiError ? decide.error.message : 'No se pudo guardar.'}
        </p>
      )}
    </div>
  )
}
