from pathlib import Path
p=Path('/workspace/MCFlow/projects/e4c_full_multikappa/rivet.cc')
s=p.read_text()

s=s.replace("""// Connected moment-nulled E4C-m constituent measurement.
// This analysis stores all kappa constituents on the same event sample.
// Null combinations are formed offline so their covariance can be estimated
// from paired job/block replicas without rerunning the shower.
""","""// Full single-axis multi-kappa E4C measurement.
// Primary channel: M_{a,kappa}=|V_{a,kappa}|^2 for each ordered reference pair.
// Axis-even, axis-odd, and cross channels are stored on the same events only
// as controls, so observable-design changes never require a new shower sample.
""")
s=s.replace('namespace E4CM {','namespace E4CF {')
s=s.replace('E4CM::','E4CF::')
s=s.replace('E4C_M_MULTI_KAPPA','E4C_FULL_MULTI_KAPPA')
s=s.replace('E4C-m','E4C-full')

old="""  inline const std::array<const char*,NK>& channelLabels() {
    static const std::array<const char*,NK> values{{
      "Mminus_k100", "Mminus_k110", "Mminus_k125", "Mminus_k200", "Mminus_k300"
    }};
    return values;
  }

  inline const std::array<const char*,NK>& referenceSafeChannelLabels() {
    static const std::array<const char*,NK> values{{
      "Mrs_k100", "Mrs_k110", "Mrs_k125", "Mrs_k200", "Mrs_k300"
    }};
    return values;
  }
"""
new="""  inline const std::array<const char*,NK>& fullChannelLabels() {
    static const std::array<const char*,NK> values{{
      "Mfull_k100", "Mfull_k110", "Mfull_k125", "Mfull_k200", "Mfull_k300"
    }};
    return values;
  }

  inline const std::array<const char*,NK>& evenChannelLabels() {
    static const std::array<const char*,NK> values{{
      "Meven_k100", "Meven_k110", "Meven_k125", "Meven_k200", "Meven_k300"
    }};
    return values;
  }

  inline const std::array<const char*,NK>& oddChannelLabels() {
    static const std::array<const char*,NK> values{{
      "Modd_k100", "Modd_k110", "Modd_k125", "Modd_k200", "Modd_k300"
    }};
    return values;
  }

  inline const std::array<const char*,NK>& crossChannelLabels() {
    static const std::array<const char*,NK> values{{
      "Mcross_k100", "Mcross_k110", "Mcross_k125", "Mcross_k200", "Mcross_k300"
    }};
    return values;
  }
"""
assert old in s
s=s.replace(old,new)

old="""  inline Real minusMoment(const FamilyVectors& va, const FamilyVectors& vb, std::size_t ik) {
    if (ik >= NK) throw std::runtime_error("E4C-full kappa index out of range");
    return (va[ik]-vb[ik]).norm2()/4;
  }

  inline Real referenceSafeMoment(Real minus, Real tau, Real Q) {
    if (!std::isfinite(minus) || !std::isfinite(tau) || !std::isfinite(Q) || Q <= 0)
      throw std::runtime_error("E4C-full reference-safe moment requires finite inputs and Q>0");
    return minus-Q*Q*tau;
  }
"""
new="""  inline Real fullMoment(const FamilyVectors& va, std::size_t ik) {
    if (ik >= NK) throw std::runtime_error("E4C-full kappa index out of range");
    return va[ik].norm2();
  }

  inline Real evenMoment(const FamilyVectors& va, const FamilyVectors& vb, std::size_t ik) {
    if (ik >= NK) throw std::runtime_error("E4C-full kappa index out of range");
    return (va[ik]+vb[ik]).norm2()/4;
  }

  inline Real oddMoment(const FamilyVectors& va, const FamilyVectors& vb, std::size_t ik) {
    if (ik >= NK) throw std::runtime_error("E4C-full kappa index out of range");
    return (va[ik]-vb[ik]).norm2()/4;
  }

  inline Real crossMoment(const FamilyVectors& va, const FamilyVectors& vb, std::size_t ik) {
    if (ik >= NK) throw std::runtime_error("E4C-full kappa index out of range");
    return va[ik].dot(vb[ik]);
  }
"""
assert old in s
s=s.replace(old,new)

old="""          _kernels[0][slot] += double(pair.anchorWeight);
          for (std::size_t ik=0; ik<E4CF::NK; ++ik) {
            const E4CF::Real value = E4CF::minusMoment(vectors[a],vectors[b],ik);
            if (!(value >= 0) || !std::isfinite(value))
              throw std::runtime_error("E4C-full negative/non-finite constituent moment");
            _kernels[ik+1][slot] += double(pair.anchorWeight*value);
            const E4CF::Real rs = E4CF::referenceSafeMoment(value,pair.tau,_Q);
            if (!std::isfinite(rs))
              throw std::runtime_error("E4C-full non-finite reference-safe constituent moment");
            _kernels[1+E4CF::NK+ik][slot] += double(pair.anchorWeight*rs);
          }
"""
new="""          _kernels[0][slot] += double(pair.anchorWeight);
          for (std::size_t ik=0; ik<E4CF::NK; ++ik) {
            const E4CF::Real full = E4CF::fullMoment(vectors[a],ik);
            const E4CF::Real even = E4CF::evenMoment(vectors[a],vectors[b],ik);
            const E4CF::Real odd = E4CF::oddMoment(vectors[a],vectors[b],ik);
            const E4CF::Real cross = E4CF::crossMoment(vectors[a],vectors[b],ik);
            if (!(full >= 0) || !(even >= 0) || !(odd >= 0) ||
                !std::isfinite(full) || !std::isfinite(even) ||
                !std::isfinite(odd) || !std::isfinite(cross))
              throw std::runtime_error("E4C-full non-finite family moment");
            _kernels[1+ik][slot] += double(pair.anchorWeight*full);
            _kernels[1+E4CF::NK+ik][slot] += double(pair.anchorWeight*even);
            _kernels[1+2*E4CF::NK+ik][slot] += double(pair.anchorWeight*odd);
            _kernels[1+3*E4CF::NK+ik][slot] += double(pair.anchorWeight*cross);
          }
"""
assert old in s
s=s.replace(old,new)

s=s.replace('MSG_INFO("E4C-full multi-kappa constituents: Q=" << _Q << " GeV, kappa={1,1.1,1.25,2,3}, "',
            'MSG_INFO("E4C full single-axis multi-kappa: Q=" << _Q << " GeV, kappa={1,1.1,1.25,2,3}, "')

start=s.index('    void writeFamilyMetadata(H5::H5File& file) const {')
end=s.index('\n    void writeHdf5Output() const {',start)
newblock="""    void writeFamilyMetadata(H5::H5File& file) const {
      H5::Group family = file.createGroup("family");
      std::vector<double> kv;
      std::vector<std::string> fullChannels, evenChannels, oddChannels, crossChannels;
      for (std::size_t ik=0; ik<E4CF::NK; ++ik) {
        kv.push_back(static_cast<double>(E4CF::kappas()[ik]));
        fullChannels.emplace_back(E4CF::fullChannelLabels()[ik]);
        evenChannels.emplace_back(E4CF::evenChannelLabels()[ik]);
        oddChannels.emplace_back(E4CF::oddChannelLabels()[ik]);
        crossChannels.emplace_back(E4CF::crossChannelLabels()[ik]);
      }
      mf::writeDataset(family,"kappa_values",kv,0);
      writeStringDataset(family,"full_channel_names",fullChannels);
      writeStringDataset(family,"even_channel_names",evenChannels);
      writeStringDataset(family,"odd_channel_names",oddChannels);
      writeStringDataset(family,"cross_channel_names",crossChannels);
      writeStringAttribute(family,"primary_measurement","full single-axis M_{a,kappa}=|V_{a,kappa}|^2; controls are even/odd/cross");
      writeStringAttribute(family,"combination_policy","store all kappa constituents and controls on the same events; form signed combinations offline");

      H5::Group nulls = file.createGroup("null_basis");
      const std::vector<std::string> nullNames{
        "two_k125_k200", "two_k125_k300", "three_k110_k200_k300"
      };
      writeStringDataset(nulls,"names",nullNames);
      const std::vector<double> coeffs{
        0,0,8.0/3.0,-5.0/3.0,0,
        0,0,12.0/7.0,0,-5.0/7.0,
        0,10000.0/3477.0,0,-451.0/183.0,682.0/1159.0
      };
      writeDoubleDataset(nulls,"coefficients",{3,E4CF::NK},{3,E4CF::NK},coeffs,"null,kappa");
      mf::writeDataset(nulls,"c_values",
        std::vector<double>{13.0/10.0,17.0/15.0,6355.0/4026.0},0);
      writeLongLongDataset(nulls,"first_residual_moment_power",std::vector<long long>{4,4,6});
      writeStringAttribute(nulls,"definition",
        "rows act on family kappa order {1,1.1,1.25,2,3}; rows are normalized sum a_i=1 and are not precombined");
    }
"""
s=s[:start]+newblock+s[end:]

s=s.replace('writeStringAttribute(file,"measurement","connected_axis_odd_multikappa_constituents");',
            'writeStringAttribute(file,"measurement","full_single_axis_multikappa_with_axis_controls");')
s=s.replace('''        writeStringAttribute(file,"definition_source","e4c_m.tex: M_{-,kappa}=|V_{a,kappa}-V_{b,kappa}|^2/4; reference-safe companion M_rs=M_- - Q^2*tau");
        writeStringAttribute(file,"reference_safe_definition","M_rs,kappa=M_-,kappa-Q^2*tau evaluated exactly for each ordered reference pair before histogramming");''',
            '''        writeStringAttribute(file,"definition_source","full single-axis family: M_{a,kappa}=|V_{a,kappa}|^2; even/odd/cross stored only as same-event controls");''')
s=s.replace('writeStringAttribute(file,"s_q2_average_definition","4*normalized Mminus bin integral/delta(qT^2); finite-bin average of S_{-,kappa}");',
            'writeStringAttribute(file,"s_q2_average_definition","4*normalized moment bin integral/delta(qT^2); finite-bin average for each stored moment channel");')
s=s.replace('writeIntegerAttribute(file,"schema_version",2);','writeIntegerAttribute(file,"schema_version",1);')

start=s.index('    const std::vector<std::string> _names{')
end=s.index('\n\n    double _Q=',start)
newnames="""    const std::vector<std::string> _names{
      "E2",
      "Mfull_k100", "Mfull_k110", "Mfull_k125", "Mfull_k200", "Mfull_k300",
      "Meven_k100", "Meven_k110", "Meven_k125", "Meven_k200", "Meven_k300",
      "Modd_k100", "Modd_k110", "Modd_k125", "Modd_k200", "Modd_k300",
      "Mcross_k100", "Mcross_k110", "Mcross_k125", "Mcross_k200", "Mcross_k300"
    };
    const std::vector<std::string> _definitions{
      "1",
      "Mfull kappa=1: |V_a|^2; primary single-axis regression channel",
      "Mfull kappa=1.1: |V_a|^2; primary single-axis channel",
      "Mfull kappa=1.25: |V_a|^2; primary single-axis channel",
      "Mfull kappa=2: |V_a|^2; primary single-axis channel",
      "Mfull kappa=3: |V_a|^2; primary single-axis channel",
      "Meven kappa=1: |V_a+V_b|^2/4; control",
      "Meven kappa=1.1: |V_a+V_b|^2/4; control",
      "Meven kappa=1.25: |V_a+V_b|^2/4; control",
      "Meven kappa=2: |V_a+V_b|^2/4; control",
      "Meven kappa=3: |V_a+V_b|^2/4; control",
      "Modd kappa=1: |V_a-V_b|^2/4; old M_- control only",
      "Modd kappa=1.1: |V_a-V_b|^2/4; old M_- control only",
      "Modd kappa=1.25: |V_a-V_b|^2/4; old M_- control only",
      "Modd kappa=2: |V_a-V_b|^2/4; old M_- control only",
      "Modd kappa=3: |V_a-V_b|^2/4; old M_- control only",
      "Mcross kappa=1: V_a dot V_b; signed control",
      "Mcross kappa=1.1: V_a dot V_b; signed control",
      "Mcross kappa=1.25: V_a dot V_b; signed control",
      "Mcross kappa=2: V_a dot V_b; signed control",
      "Mcross kappa=3: V_a dot V_b; signed control"
    };"""
s=s[:start]+newnames+s[end:]

p.write_text(s)
print(p)
