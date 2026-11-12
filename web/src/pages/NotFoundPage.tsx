import { Link } from 'react-router';
import { PageHeading } from '../components/Layout.tsx';

export default function NotFoundPage() {
  return (
    <>
      <PageHeading title="Page not found" />
      <p>
        There is no page at this address. <Link to="/">Go to the pod overview</Link>.
      </p>
    </>
  );
}
