'use client';
import React, { useEffect, useState } from 'react';
import Link from 'next/link';
import PageHeader from '@/components/ui/PageHeader';
import Section from '@/components/ui/Section';
import Card from '@/components/ui/Card';
import Btn from '@/components/ui/Button';
import Input from '@/components/ui/Input';
import { IconBuilding, IconPlus, IconAlert } from '@/components/icons';
import { formatError } from '@/lib/connectError';
import { rpcFetch } from '@/lib/rpc';
import { getUsername } from '@/lib/auth';

interface Org { id: string; username: string; description?: string; url?: string; }

export default function PageOrganizations() {
  const [orgs, setOrgs] = useState<Org[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [showNew, setShowNew] = useState(false);
  const [newName, setNewName] = useState('');
  const [newDescription, setNewDescription] = useState('');
  const [creating, setCreating] = useState(false);

  const loadOrgs = async () => {
    const username = getUsername();
    if (!username) { setLoading(false); return; }
    try {
      const data = await rpcFetch<{ organizations: Org[] }>('/hades.api.identity.v1.OrgService/GetUserOrgs', { username });
      setOrgs(data.organizations || []);
    } catch (e) { setError(formatError(e)); } finally { setLoading(false); }
  };

  useEffect(() => { loadOrgs(); }, []);

  const createOrg = async () => {
    if (!newName) return;
    setCreating(true);
    setError('');
    try {
      await rpcFetch('/hades.api.identity.v1.OrgService/CreateOrg', { name: newName, description: newDescription, url: '' });
      setShowNew(false); setNewName(''); setNewDescription('');
      await loadOrgs();
    } catch (e) { setError(formatError(e)); } finally { setCreating(false); }
  };

  return (
    <div style={{ flex: 1, overflowY: 'auto' }}>
      <PageHeader title="Organizations" subtitle="Organizations you belong to. The creator becomes the first admin." actions={<Btn variant="primary" icon={<IconPlus size={13}/>} onClick={() => setShowNew(v => !v)}>New Organization</Btn>}/>
      <Section>
        {error && <div style={{ padding: '10px 14px', borderRadius: 6, background: 'var(--c-danger-bg)', border: '1px solid var(--c-danger)', color: 'var(--c-danger)', fontSize: 13, marginBottom: 16, display: 'flex', gap: 8 }}><IconAlert size={14}/>{error}</div>}

        {showNew && (
          <Card style={{ padding: '20px', marginBottom: 16 }}>
            <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--c-fg)', marginBottom: 16 }}>Create organization</div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <div><label style={{ fontSize: 12, fontWeight: 500, color: 'var(--c-fg-muted)', display: 'block', marginBottom: 6 }}>Name</label><Input value={newName} onChange={setNewName} placeholder="e.g. acme"/></div>
              <div><label style={{ fontSize: 12, fontWeight: 500, color: 'var(--c-fg-muted)', display: 'block', marginBottom: 6 }}>Description (optional)</label><Input value={newDescription} onChange={setNewDescription} placeholder="What this organization is for"/></div>
              <div style={{ display: 'flex', gap: 8 }}>
                <Btn variant="primary" onClick={createOrg} disabled={creating || !newName}>{creating ? 'Creating…' : 'Create Organization'}</Btn>
                <Btn variant="ghost" onClick={() => { setShowNew(false); setNewName(''); setNewDescription(''); }}>Cancel</Btn>
              </div>
            </div>
          </Card>
        )}

        {loading ? (
          <div style={{ textAlign: 'center', padding: 40, color: 'var(--c-fg-muted)', fontSize: 13 }}>Loading organizations…</div>
        ) : orgs.length === 0 ? (
          <div style={{ textAlign: 'center', padding: 40, color: 'var(--c-fg-muted)', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 12 }}>
            <IconBuilding size={28} style={{ opacity: 0.4 }}/><div style={{ fontSize: 14, fontWeight: 500 }}>No organizations yet</div><div style={{ fontSize: 13, color: 'var(--c-fg-subtle)' }}>Create one to share modules with a team.</div>
          </div>
        ) : (
          <div style={{ border: '1px solid var(--c-border)', borderRadius: 8, overflow: 'hidden' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ borderBottom: '1px solid var(--c-border)', background: 'var(--c-bg-overlay)' }}>
                  {['Name', 'Description', ''].map(h => <th key={h} style={{ padding: '10px 16px', textAlign: 'left', fontSize: 11, fontWeight: 600, color: 'var(--c-fg-muted)', letterSpacing: 0.5, textTransform: 'uppercase' }}>{h}</th>)}
                </tr>
              </thead>
              <tbody>
                {orgs.map((org, i) => (
                  <tr key={org.id} style={{ borderBottom: i < orgs.length - 1 ? '1px solid var(--c-border-muted)' : 'none' }}>
                    <td style={{ padding: '12px 16px', fontSize: 13, color: 'var(--c-fg)', fontWeight: 500 }}>{org.username}</td>
                    <td style={{ padding: '12px 16px', fontSize: 13, color: 'var(--c-fg-subtle)' }}>{org.description || '—'}</td>
                    <td style={{ padding: '12px 16px' }}><Link href={`/${org.username}`} style={{ fontSize: 13, color: 'var(--c-accent)', textDecoration: 'none' }}>View →</Link></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Section>
    </div>
  );
}
