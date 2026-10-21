'use client';
import React, { useCallback, useEffect, useState } from 'react';
import PageHeader from '@/components/ui/PageHeader';
import Section from '@/components/ui/Section';
import Card from '@/components/ui/Card';
import Btn from '@/components/ui/Button';
import Badge from '@/components/ui/Badge';
import Input from '@/components/ui/Input';
import { IconAlert, IconSearch, IconShield } from '@/components/icons';
import { formatError } from '@/lib/connectError';
import { rpcFetch } from '@/lib/rpc';
import { useAuthStore } from '@/stores/authStore';
import { getUsername } from '@/lib/auth';

const SVC = '/hades.api.identity.v1.AdminService';

type UserState = 'USER_STATE_ACTIVE' | 'USER_STATE_DEACTIVATED' | 'USER_STATE_UNSPECIFIED';

// Connect's JSON encoding leaves out fields at their zero value, so every
// boolean and count here may be absent.
interface AdminUser {
  user: { id: string; username: string; email?: string; state?: UserState; createTime?: string };
  superadmin?: boolean;
  superadminFromConfig?: boolean;
  moduleCount?: number;
}

function fmtDate(ts?: string): string {
  if (!ts) return '-';
  try { return new Date(ts).toLocaleDateString(); } catch { return ts; }
}

const isDeactivated = (u: AdminUser) => u.user.state === 'USER_STATE_DEACTIVATED';

export default function PageAdminUsers() {
  // The store is empty after a full page load; the username cookie is what the
  // sidebar falls back to as well.
  const { username } = useAuthStore();
  const me = username || getUsername();
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [query, setQuery] = useState('');
  const [nextPage, setNextPage] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  // Username whose action is in flight, so only that row's buttons disable.
  const [busy, setBusy] = useState<string | null>(null);
  // Username awaiting a second click to confirm deletion.
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  const load = useCallback(async (q: string, pageToken = '') => {
    setLoading(true);
    try {
      const data = await rpcFetch<{ users?: AdminUser[]; nextPageToken?: string }>(`${SVC}/ListUsers`, { query: q, pageToken });
      setUsers(prev => (pageToken ? [...prev, ...(data.users || [])] : data.users || []));
      setNextPage(data.nextPageToken || '');
      setError('');
    } catch (e) { setError(formatError(e)); } finally { setLoading(false); }
  }, []);

  useEffect(() => {
    const t = setTimeout(() => { load(query); }, 250);
    return () => clearTimeout(t);
  }, [query, load]);

  // Every mutation returns the updated record (or nothing, for a delete); the
  // row is replaced from the response rather than refetching the page.
  const act = async (username: string, fn: () => Promise<AdminUser | null>) => {
    setBusy(username); setError('');
    try {
      const updated = await fn();
      setUsers(prev => updated
        ? prev.map(u => (u.user.username === username ? updated : u))
        : prev.filter(u => u.user.username !== username));
    } catch (e) { setError(formatError(e)); } finally { setBusy(null); setConfirmDelete(null); }
  };

  const setState = (u: AdminUser, state: UserState) => act(u.user.username, async () =>
    (await rpcFetch<{ user: AdminUser }>(`${SVC}/SetUserState`, { username: u.user.username, state })).user);

  const setSuperadmin = (u: AdminUser, superadmin: boolean) => act(u.user.username, async () =>
    (await rpcFetch<{ user: AdminUser }>(`${SVC}/SetSuperadmin`, { username: u.user.username, superadmin })).user);

  const remove = (u: AdminUser) => act(u.user.username, async () => {
    await rpcFetch(`${SVC}/DeleteUser`, { username: u.user.username });
    return null;
  });

  return (
    <div style={{ flex: 1, overflowY: 'auto' }}>
      <PageHeader title="Users" subtitle="Deactivate, delete, and promote accounts on this registry. Visible to superadmins only."/>
      <Section>
        <div style={{ marginBottom: 16, maxWidth: 360 }}>
          <Input value={query} onChange={setQuery} placeholder="Search by username" prefix={<IconSearch size={13}/>} ariaLabel="Search users"/>
        </div>

        {error && <div role="alert" style={{ padding: '10px 14px', borderRadius: 6, background: 'var(--c-danger-bg)', border: '1px solid var(--c-danger)', color: 'var(--c-danger)', fontSize: 13, marginBottom: 16, display: 'flex', gap: 8, alignItems: 'center' }}><IconAlert size={14}/>{error}</div>}

        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {users.map(u => {
            const name = u.user.username;
            const self = name === me;
            const rowBusy = busy === name;
            const modules = u.moduleCount || 0;
            const deleteBlocked = self ? 'You cannot delete your own account'
              : u.superadminFromConfig ? 'Listed in opa.superAdmins in the server configuration'
              : modules > 0 ? `Owns ${modules} module${modules === 1 ? '' : 's'}, which would be deleted with it`
              : '';
            return (
              <Card key={u.user.id} style={{ padding: '14px 20px', display: 'flex', gap: 16, alignItems: 'center', flexWrap: 'wrap', opacity: isDeactivated(u) ? 0.75 : 1 }}>
                <div style={{ flex: 1, minWidth: 220 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4, flexWrap: 'wrap' }}>
                    <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--c-fg)', fontFamily: "'IBM Plex Mono', monospace" }}>{name}</span>
                    {self && <Badge variant="blue">You</Badge>}
                    {u.superadmin && <Badge variant="purple"><IconShield size={10}/>Superadmin{u.superadminFromConfig ? ' (config)' : ''}</Badge>}
                    {isDeactivated(u) ? <Badge variant="red">Deactivated</Badge> : <Badge variant="green">Active</Badge>}
                  </div>
                  <div style={{ display: 'flex', gap: 16, fontSize: 12, color: 'var(--c-fg-subtle)', flexWrap: 'wrap' }}>
                    {u.user.email && <span>{u.user.email}</span>}
                    <span>{modules} module{modules === 1 ? '' : 's'}</span>
                    <span>Joined {fmtDate(u.user.createTime)}</span>
                  </div>
                </div>

                <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                  {u.superadmin ? (
                    <span title={self ? 'You cannot demote yourself' : u.superadminFromConfig ? 'Listed in opa.superAdmins in the server configuration' : undefined}>
                      <Btn size="sm" onClick={() => setSuperadmin(u, false)} disabled={rowBusy || self || !!u.superadminFromConfig}>Demote</Btn>
                    </span>
                  ) : (
                    <span title={isDeactivated(u) ? 'Reactivate the account first' : undefined}>
                      <Btn size="sm" onClick={() => setSuperadmin(u, true)} disabled={rowBusy || isDeactivated(u)}>Make superadmin</Btn>
                    </span>
                  )}
                  {isDeactivated(u) ? (
                    <Btn size="sm" variant="success" onClick={() => setState(u, 'USER_STATE_ACTIVE')} disabled={rowBusy}>Reactivate</Btn>
                  ) : (
                    <span title={self ? 'You cannot deactivate your own account' : undefined}>
                      <Btn size="sm" onClick={() => setState(u, 'USER_STATE_DEACTIVATED')} disabled={rowBusy || self}>Deactivate</Btn>
                    </span>
                  )}
                  <span title={deleteBlocked || undefined}>
                    {confirmDelete === name ? (
                      <Btn size="sm" variant="danger" onClick={() => remove(u)} disabled={rowBusy}>{rowBusy ? 'Deleting…' : 'Confirm delete'}</Btn>
                    ) : (
                      <Btn size="sm" variant="danger" onClick={() => setConfirmDelete(name)} disabled={rowBusy || !!deleteBlocked}>Delete</Btn>
                    )}
                  </span>
                </div>
              </Card>
            );
          })}
          {!loading && users.length === 0 && !error && <div style={{ textAlign: 'center', padding: 40, color: 'var(--c-fg-muted)', fontSize: 13 }}>No users found.</div>}
          {loading && <div style={{ textAlign: 'center', padding: 24, color: 'var(--c-fg-muted)', fontSize: 13 }}>Loading users…</div>}
          {!loading && nextPage && (
            <div style={{ textAlign: 'center' }}><Btn onClick={() => load(query, nextPage)}>Load more</Btn></div>
          )}
        </div>

        <div style={{ marginTop: 24, padding: '16px 20px', borderRadius: 8, background: 'var(--c-bg-overlay)', border: '1px solid var(--c-border)', fontSize: 13, color: 'var(--c-fg-muted)', lineHeight: 1.6 }}>
          Deactivating an account blocks sign-in and stops its sessions and API tokens immediately; reactivating restores them.
          Deleting is permanent and removes the account&apos;s sessions, tokens, organization memberships and permissions.
          Superadmins have every permission on the registry.
        </div>
      </Section>
    </div>
  );
}
