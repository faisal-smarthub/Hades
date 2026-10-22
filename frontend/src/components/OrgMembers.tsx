'use client';
import React, { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import Avatar from '@/components/ui/Avatar';
import Badge from '@/components/ui/Badge';
import Btn from '@/components/ui/Button';
import Card from '@/components/ui/Card';
import EmptyState from '@/components/ui/EmptyState';
import Input from '@/components/ui/Input';
import Section from '@/components/ui/Section';
import { IconAlert, IconShield, IconUser } from '@/components/icons';
import { rpcFetch } from '@/lib/rpc';
import { formatError } from '@/lib/connectError';
import { getUsername } from '@/lib/auth';
import { useAuthStore } from '@/stores/authStore';

export interface OrgMember { user: { id: string; username: string }; role: string; }

const SVC = '/hades.api.identity.v1.OrgService';

const ROLES: { value: string; label: string; hint: string }[] = [
  { value: 'reader', label: 'Reader', hint: 'Read modules' },
  { value: 'member', label: 'Member', hint: 'Read and push' },
  { value: 'admin', label: 'Admin', hint: 'Everything, including members and deletion' },
];

const selectStyle: React.CSSProperties = {
  padding: '5px 8px', fontSize: 13, borderRadius: 6, border: '1px solid var(--c-border)',
  background: 'var(--c-bg-overlay)', color: 'var(--c-fg)',
};

// OrgMembers lists an organization's members. Org admins and superadmins also
// get controls to add members, change roles and remove members. The server
// decides either way: hiding the controls only spares everyone else buttons
// that would be refused.
const OrgMembers: React.FC<{ orgName: string; members: OrgMember[]; onChange: (members: OrgMember[]) => void }> = ({ orgName, members, onChange }) => {
  const router = useRouter();
  const { username } = useAuthStore();
  const me = username || getUsername();
  const [isSuperadmin, setIsSuperadmin] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null);
  const [error, setError] = useState('');
  const [newUser, setNewUser] = useState('');
  const [newRole, setNewRole] = useState('member');

  useEffect(() => {
    rpcFetch<{ isSuperadmin?: boolean }>('/hades.api.authorization.v1.Authorization/UserBySession', {})
      .then(res => setIsSuperadmin(!!res.isSuperadmin))
      .catch(() => {});
  }, []);

  const canManage = isSuperadmin || members.some(m => m.user?.username === me && m.role === 'admin');
  const adminCount = members.filter(m => m.role === 'admin').length;

  const reload = () => rpcFetch<{ members: OrgMember[] }>(`${SVC}/ListOrgMembers`, { orgName })
    .then(res => onChange(res.members || []));

  const run = async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key); setError('');
    try { await fn(); await reload(); return true; }
    catch (e) { setError(formatError(e)); return false; }
    finally { setBusy(null); setConfirmRemove(null); }
  };

  const setRole = (name: string, role: string) => run(name, () => rpcFetch(`${SVC}/AddOrgMember`, { orgName, username: name, role }));
  const remove = (name: string) => run(name, () => rpcFetch(`${SVC}/RemoveOrgMember`, { orgName, username: name }));
  const add = async () => {
    const name = newUser.trim().toLowerCase();
    if (!name) return;
    if (await run('__add', () => rpcFetch(`${SVC}/AddOrgMember`, { orgName, username: name, role: newRole }))) setNewUser('');
  };

  return (
    <Section>
      {canManage && (
        <Card style={{ padding: '14px 18px', marginBottom: 16, display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
          <div style={{ flex: 1, minWidth: 200 }}>
            <Input value={newUser} onChange={setNewUser} placeholder="Username to add or change" ariaLabel="Username to add" prefix={<IconUser size={13}/>}/>
          </div>
          <select value={newRole} onChange={e => setNewRole(e.target.value)} style={selectStyle} aria-label="Role for the new member">
            {ROLES.map(r => <option key={r.value} value={r.value}>{r.label}</option>)}
          </select>
          <Btn variant="primary" size="sm" onClick={add} disabled={busy === '__add' || !newUser.trim()}>{busy === '__add' ? 'Adding…' : 'Add member'}</Btn>
        </Card>
      )}

      {error && <div role="alert" style={{ padding: '10px 14px', borderRadius: 6, background: 'var(--c-danger-bg)', border: '1px solid var(--c-danger)', color: 'var(--c-danger)', fontSize: 13, marginBottom: 16, display: 'flex', gap: 8, alignItems: 'center' }}><IconAlert size={14}/>{error}</div>}

      {members.length === 0 ? <EmptyState icon={<IconUser size={40}/>} title="No members"/> : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {members.map(m => {
            const name = m.user?.username || '';
            const role = m.role || 'member';
            const lastAdmin = role === 'admin' && adminCount <= 1;
            const rowBusy = busy === name;
            return (
              <Card key={m.user?.id || name} style={{ padding: '10px 16px', display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flex: 1, minWidth: 180 }}>
                  <Avatar initials={name.slice(0, 2).toUpperCase()} size={24}/>
                  <span style={{ cursor: 'pointer', color: 'var(--c-accent)', fontSize: 13 }} onClick={() => router.push(`/${name}`)}>@{name}</span>
                  {name === me && <Badge variant="blue">You</Badge>}
                </div>
                {canManage ? (
                  <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                    <span title={lastAdmin ? 'The last admin cannot be demoted; make someone else an admin first' : undefined}>
                      <select value={role} disabled={rowBusy || lastAdmin} onChange={e => setRole(name, e.target.value)} style={selectStyle} aria-label={`Role of ${name}`}>
                        {ROLES.map(r => <option key={r.value} value={r.value} title={r.hint}>{r.label}</option>)}
                      </select>
                    </span>
                    <span title={lastAdmin ? 'The last admin cannot be removed' : undefined}>
                      {confirmRemove === name ? (
                        <Btn size="sm" variant="danger" onClick={() => remove(name)} disabled={rowBusy}>{rowBusy ? 'Removing…' : 'Confirm remove'}</Btn>
                      ) : (
                        <Btn size="sm" variant="danger" onClick={() => setConfirmRemove(name)} disabled={rowBusy || lastAdmin}>Remove</Btn>
                      )}
                    </span>
                  </div>
                ) : (
                  <Badge variant={role === 'admin' ? 'purple' : 'default'}><IconShield size={10}/> {role}</Badge>
                )}
              </Card>
            );
          })}
        </div>
      )}
    </Section>
  );
};

export default OrgMembers;
