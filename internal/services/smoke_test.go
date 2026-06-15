package services
import ("testing")
func TestServicesSmoke(t *testing.T){
  c,err:=NewClient(); if err!=nil{ t.Skipf("no system bus: %v",err) }
  defer c.Close()
  svcs,err:=c.List(true); if err!=nil{ t.Fatalf("List: %v",err) }
  t.Logf("got %d .service units; first 8:",len(svcs))
  for i:=0;i<8 && i<len(svcs);i++{s:=svcs[i]
    t.Logf("  %-32s status=%d active=%-9s sub=%-8s enabled=%-9s  %s",
      s.Name,s.Status,s.Active,s.Sub,s.Enabled,s.Description)}
}
