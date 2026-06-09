import { useMemo, useState } from 'react';
import { Loader2, Search } from 'lucide-react';
import { NavLink } from 'react-router';
import { WAContactKind } from '../proto/byte/v/forge/waapp/v1/contacts';
import type { WaContact } from './wa-chat-model';
import { formatChatTime } from './wa-chat-model';
import { WhatsAppIcon } from './wa-brand-icon';
import { waContactPath } from './wa-route-paths';
import { Badge } from './ui';

export function WaContactList({ accountID, contacts, selectedID, loading, error }: { accountID: string; contacts: WaContact[]; selectedID: string; loading: boolean; error?: string }) {
  const [query, setQuery] = useState('');
  const visibleContacts = useMemo(() => filterContacts(contacts, query), [contacts, query]);
  return (
    <aside className="grid min-h-0 grid-rows-[auto_auto_1fr] overflow-hidden border-r border-border bg-card">
      <header className="flex h-16 items-center justify-between px-4">
        <div><h2 className="text-base font-semibold">联系人</h2><p className="text-xs text-muted-foreground">{contacts.length} 个会话</p></div>
        {loading && <Loader2 className="size-4 animate-spin text-muted-foreground" />}
      </header>
      <div className="px-3 pb-3">
        <label className="flex h-10 items-center gap-2 rounded-xl bg-muted/50 px-3 text-sm text-muted-foreground"><Search size={15} /><input className="min-w-0 flex-1 bg-transparent text-foreground outline-none placeholder:text-muted-foreground" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索联系人" /></label>
      </div>
      <div className="min-h-0 overflow-y-auto p-2">
        {error && <p className="rounded-xl border border-destructive/30 p-3 text-sm text-destructive">{error}</p>}
        {!loading && !error && contacts.length === 0 && <p className="p-4 text-sm text-muted-foreground">暂无联系人，收到消息后会显示在这里。</p>}
        {!loading && !error && contacts.length > 0 && visibleContacts.length === 0 && <p className="p-4 text-sm text-muted-foreground">没有匹配联系人。</p>}
        {visibleContacts.map((contact) => <ContactLink key={contact.id} accountID={accountID} contact={contact} selected={contact.id === selectedID} />)}
      </div>
    </aside>
  );
}

function ContactLink({ accountID, contact, selected }: { accountID: string; contact: WaContact; selected: boolean }) {
  return (
    <NavLink className={({ isActive }) => `mb-1 grid w-full grid-cols-[42px_1fr_auto] items-center gap-3 rounded-2xl px-3 py-2.5 text-left transition hover:bg-muted/60 ${selected || isActive ? 'bg-primary/10' : ''}`} to={waContactPath(accountID, contact.id)}>
      <ContactAvatar contact={contact} />
      <span className="min-w-0">
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate text-sm font-medium">{contact.title}</span>
          <ContactKindBadge kind={contact.kind} />
        </span>
        <span className="block truncate text-xs text-muted-foreground">{contact.subtitle}</span>
      </span>
      <span className="grid justify-items-end gap-1">
        <time className="text-[11px] text-muted-foreground">{formatChatTime(contact.lastAt)}</time>
        {contact.count > 0 && <Badge variant="outline">{contact.count}</Badge>}
      </span>
    </NavLink>
  );
}

function ContactAvatar({ contact }: { contact: WaContact }) {
  const [failedURL, setFailedURL] = useState('');
  if (contact.profilePictureURL && failedURL !== contact.profilePictureURL) {
    return <img className="size-10 rounded-full border border-border object-cover" src={contact.profilePictureURL} alt={contact.title} loading="lazy" onError={() => setFailedURL(contact.profilePictureURL || '')} />;
  }
  return <span className="grid size-10 place-items-center rounded-full bg-emerald-50"><WhatsAppIcon className="size-6" title={contact.title} /></span>;
}

function ContactKindBadge({ kind }: { kind: WAContactKind }) {
  const label = kindLabel(kind);
  if (!label) return null;
  return <span className="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">{label}</span>;
}

function filterContacts(contacts: WaContact[], query: string) {
  const needle = query.trim().toLowerCase();
  if (!needle) return contacts;
  return contacts.filter((contact) => `${contact.title} ${contact.subtitle} ${contact.id}`.toLowerCase().includes(needle));
}

function kindLabel(kind: WAContactKind) {
  if (kind === WAContactKind.WA_CONTACT_KIND_GROUP) return '群';
  if (kind === WAContactKind.WA_CONTACT_KIND_BUSINESS) return '企';
  if (kind === WAContactKind.WA_CONTACT_KIND_SYSTEM) return '系统';
  if (kind === WAContactKind.WA_CONTACT_KIND_INTEROP) return '互通';
  return '';
}
